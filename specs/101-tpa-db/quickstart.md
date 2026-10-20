# Quickstart: tpa-db

**Feature**: `101-tpa-db` · Sign a corpus, drop it, verify it activated, roll it back, read the status.

Everything here works with **no network** — that is SC-003, not a footnote. The fetch path is Spec 087's (plan D11).

## 0. Build

```bash
go build -o mcpproxy ./cmd/mcpproxy
```

Use an isolated instance for all of this — a high port and a scratch `--data-dir` **and** `--config`. The watermark, ratchet and pin are per-data-directory, so testing against your real one leaves state behind that is deliberately hard to undo.

```bash
SCRATCH=$(mktemp -d)
cat > "$SCRATCH/config.json" <<'EOF'
{ "listen": "127.0.0.1:18701", "api_key": "dev", "mcpServers": [] }
EOF
```

## 1. Generate a signing key

```bash
KG=$(go run ./cmd/tpa-db keygen --out "$SCRATCH/keys")
echo "$KG"
# public_key: <base64 of the 32-byte public key>
# key_id:     <64 lowercase hex>
PUBKEY=$(sed -n 's/^public_key: //p' <<<"$KG")
KEY_ID=$(sed -n 's/^key_id: //p' <<<"$KG")
# keys/operator.key (private, 0600) and keys/operator.pub were written; keygen never overwrites
```

The **key id is a fingerprint of the key**, never a label you choose (FR-003): the lowercase-hex SHA-256 of the 32-byte raw public key. That is what makes "the manifest names one publisher, the bytes are signed by another" detectable rather than cosmetic. Config holds the **public key** (`PUBKEY`); the manifest holds the **key id**.

## 2. Sign a bundle

The signer never edits the bundle. You author `sequence`, `trust_epoch`, `key_id` and `generated_at` in the bundle's own manifest, then sign the exact bytes. The seed corpus does not carry these yet, so stamp them with `jq`. The sequence is a decimal **string**, and it must be strictly above `tpa_operator_baseline` (step 3 uses baseline 1, so this bundle is sequence 2):

```bash
jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "2", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   internal/security/scanner/bundled/scanner-bundle.json > "$SCRATCH/scanner-bundle.json"

go run ./cmd/tpa-db sign --bundle "$SCRATCH/scanner-bundle.json" --key "$SCRATCH/keys/operator.key"
# writes scanner-bundle.json.sig next to the bundle
go run ./cmd/tpa-db verify --bundle "$SCRATCH/scanner-bundle.json" --signer "$PUBKEY"   # OK
```

`sign` refuses a bundle whose manifest `key_id` is not the id of the signing key (`key_id_mismatch`): every loader would reject that pair. It does not run the detection-quality gate in this release; gate-before-sign arrives with User Story 2.

Inspect the sidecar; it is one line, four keys, nothing else ([contract](contracts/sidecar-format.md)):

```bash
cat "$SCRATCH/scanner-bundle.json.sig" | jq .
# sidecar_version, algorithm, key_id, signature
```

## 3. Configure the operator authority and drop the bundle

An operator-signed bundle needs these config decisions, all of them **config-resident on purpose**: the config-write attacker is out of the threat model, the bundle-path attacker is in it. `tpa_operator_signers` holds **public keys**, not key ids.

```json
{
  "security": {
    "tpa_bundle_path": "<SCRATCH>/scanner-bundle.json",
    "tpa_active_root": "operator",
    "tpa_operator_signers": ["<PUBKEY from step 1>"],
    "tpa_operator_baseline": 1,
    "tpa_operator_epoch": 0,
    "require_signed_bundle": true
  }
}
```

Declaring `tpa_operator_baseline` matters more than it looks. Without it the authority runs **explicitly degraded** rather than silently trusted: with no baseline there is no anti-downgrade at all after a data-directory wipe, and an attacker who can write the bundle path could replay any older genuinely operator-signed bundle at full coverage (FR-009b).

Start the proxy. The bundle is read at startup:

```bash
./mcpproxy serve --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" --log-level=debug
```

**There is no file watcher on the bundle**: only `mcp_config.json` is watched. After a later drop, activate it with a config reload, a restart, or the reload command, which runs the same single-flight pipeline:

```bash
./mcpproxy security bundle reload --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data"
# POST /api/v1/security/bundle/reload
```

## 4. Verify it activated

```bash
./mcpproxy security bundle -o json --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" | jq .
```

Expect `signature_verified: true`, `authority: "operator"`, `trust_epoch: 0`, `sequence: "2"`, a 64-hex `full_digest`, `coverage_degraded: false`.

**If `coverage_degraded` is true, read `degraded_reason` before anything else** — it distinguishes "you forgot the baseline" (`operator_authority_unbaselined`) from "the bundle is unsigned" (`unsigned_external_active`) from "we fell back to the embedded default" (`operator_bundle_missing` / `embedded_below_watermark`). A degraded install has `scan`-mode auto-approval suspended, which is otherwise invisible.

## 5. Watch a downgrade be refused

Keep the good bundle, then sign the same corpus at a genuinely **lower** sequence (1, which is not above the watermark 2) and put it at the configured path:

```bash
mkdir "$SCRATCH/v2" "$SCRATCH/v1"
cp "$SCRATCH"/scanner-bundle.json* "$SCRATCH/v2/"

jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "1", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   internal/security/scanner/bundled/scanner-bundle.json > "$SCRATCH/v1/scanner-bundle.json"
go run ./cmd/tpa-db sign --bundle "$SCRATCH/v1/scanner-bundle.json" --key "$SCRATCH/keys/operator.key"

cp "$SCRATCH/v1/scanner-bundle.json" "$SCRATCH/v1/scanner-bundle.json.sig" "$SCRATCH/"   # overwrite the configured path
./mcpproxy security bundle reload --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data"
```

The sequence-1 artifact is at or below the watermark, so it is refused and the current bundle keeps serving:

```bash
./mcpproxy security bundle -o json --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" | jq '{sequence, last_rejection}'
# { "sequence": "2", "last_rejection": { "reason": "downgrade", "detail": "sequence 1, watermark 2 …", "at": "…" } }
```

Nothing about this is fatal: every rejection keeps last-known-good. That is the point: a refused candidate must never leave the scanner with an empty or partial rule set.

## 6. Roll back — by full digest, not by path

Read the history to learn the digest you want:

```bash
./mcpproxy security bundle history -o json --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" \
  | jq '.[] | {sequence, full_digest, activated_at}'
```

The history only lists releases that activated, so it will not contain the sequence-1 file. Take its digest from the file you hold, which is fine here because you made it; in an incident, take it from the history or from your release record, never from the file under suspicion:

```bash
# A rollback target must sit directly in the directory of tpa_bundle_path.
cp "$SCRATCH/v1/scanner-bundle.json" "$SCRATCH/scanner-bundle.v1.json"
cp "$SCRATCH/v1/scanner-bundle.json.sig" "$SCRATCH/scanner-bundle.v1.json.sig"
V1=$(shasum -a 256 "$SCRATCH/scanner-bundle.v1.json" | cut -d' ' -f1)
./mcpproxy security bundle rollback --path "$SCRATCH/scanner-bundle.v1.json" --digest "$V1" \
  --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data"
# or, to go back to the corpus compiled into the binary: rollback --embedded --digest <embedded full_digest>
```

Give **exactly one** of `--embedded` or `--path`. The path must be a file directly in the directory of `tpa_bundle_path` (anything else is refused before it is opened, so the rollback door cannot probe other files). The path is a *hint about where to find those bytes*; the digest is the authorization. Rollback is the one path that deliberately bypasses the watermark, so identifying its target by an attacker-writable path would hand the attacker the choice of what the bypass lands on. A target below your operator baseline is refused. Supply the wrong digest and it is refused `rollback_target_mismatch` — try it, it is one command:

```bash
./mcpproxy security bundle rollback --path "$SCRATCH/scanner-bundle.v1.json" \
  --digest 0000000000000000000000000000000000000000000000000000000000000000 …   # refused (409)
```

**The 12-hex short fingerprint is not accepted here** (400, "the full digest is required"). It is 48 bits — fine as a human label, not as authorization for a watermark bypass where the attacker chooses the substituted bytes.

After a rollback the artifact is **pinned**, the release you rolled away from (sequence 2) is **deny-listed**, and the watermark stays at 2. Put the sequence-2 file back at the configured path and reload to see `deny_listed`: the next refresh cycle cannot re-activate the very thing you just rejected. Clear the pin to return to normal watermark-governed activation:

```bash
./mcpproxy security bundle unpin --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data"
```

With the pin gone, the sequence-1 bundle no longer passes the watermark, so the install falls back to the embedded default and reports the matching degraded reason.

## 7. Air-gapped check (SC-003)

Steps 1–6 with the network unavailable behave identically. That is the criterion — the file-drop path is not a degraded mode, it is the primary one, and the fetch path adds convenience rather than capability. The operator-facing version of this walkthrough is `docs/features/tpa-bundle-signing.md`.

## 8. Contributor loop (User Story 2, not in the first PR)

```bash
go run ./cmd/tpa-db validate --bundle <path>   # schema + signatures[] integrity + licences
go run ./cmd/scan-eval --gate --bundle <path>  # recall ≥ 0.90 gated, hard-negative FP ≤ 0.05
```

(amended by US2, 2026-10-20: corpus validation is `cmd/tpa-corpus`, an in-repo command separate from the signing command so the two stories do not share a `main.go`. `cmd/tpa-db` above is US1's signing tool and does not own the corpus checks.)

```bash
go run ./cmd/tpa-corpus validate --bundle <path> --dataset specs/065-evaluation-foundation/datasets/detect_corpus_v1.json
                                               # signatures[] integrity (load layer) + licence allowlist,
                                               # class vocabulary, eval pair, category registration (policy layer)
go run ./cmd/tpa-corpus build                  # compile signatures/*.json + manifest.json into scanner-bundle.json, deterministically
go run ./cmd/tpa-corpus verify                 # rebuild and byte-compare; a hand-edited scanner-bundle.json fails
```

`validate` enforces the [signatures[] contract](contracts/bundle-signatures-section.md) in both directions — every signature names a real rule, **and** every gating rule is named by exactly one signature. The second direction is the one that is easy to skip and the one that matters: without it a gating rule can ship with no provenance, no licence and no eval evidence.

`--gate` is the same code path activation runs (plan D8), so a bundle that passes here is one that will activate — the CI layer and the activation layer cannot drift apart.
