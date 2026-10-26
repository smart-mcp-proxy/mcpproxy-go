# Quickstart: tpa-db

**Feature**: `101-tpa-db` · Sign a corpus, drop it, verify it activated, roll it back, read the status.

Steps 1 to 7 work with **no network** — that is SC-003, not a footnote. The optional fetch (Spec 087, off by default, no default URL) is step 8, against a mirror on your own machine.

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

The file leg of the daily refresh (`tpa_refresh_interval`, default 24h) re-reads `tpa_bundle_path` too, so a later drop activates within a cycle with no `reload` and no restart. `mcpproxy security bundle refresh` runs that cycle on demand.

## 8. Optional fetch against a local mirror (Spec 087)

The fetch is **off by default and has no default URL**; nothing in steps 1–7 touched the network. It also works today only with operator-signed bundles, because the publisher key set embedded in the binary is empty. This step exercises it against a mirror on loopback, where plain `http` is allowed. Run it after step 4 (any state whose watermark is below 3), keeping the same `SCRATCH`, `KEY_ID`, `PUBKEY` and operator key.

Publish a newer signed pair to a directory and serve it. A path that is not `/repos/<owner>/<repo>/releases/latest` is the *direct* shape: the bundle is the URL and `<url>.sig` is its sidecar (the Releases API shape is the same with a JSON listing in front, see the operator guide):

```bash
mkdir "$SCRATCH/mirror"
jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "3", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   internal/security/scanner/bundled/scanner-bundle.json > "$SCRATCH/mirror/scanner-bundle.json"
go run ./cmd/tpa-db sign --bundle "$SCRATCH/mirror/scanner-bundle.json" --key "$SCRATCH/keys/operator.key"

uv run python -m http.server 18702 --bind 127.0.0.1 --directory "$SCRATCH/mirror" &
```

Turn the fetch on in `$SCRATCH/config.json` (the config file is hot-reloaded), keeping the operator root and signers from step 3. `tpa_fetch_enabled` could also be switched with `MCPPROXY_TPA_FETCH_ENABLED=true`, but only the config chooses the URL and the keys:

```json
{
  "security": {
    "tpa_active_root": "operator",
    "tpa_operator_signers": ["<PUBKEY from step 1>"],
    "tpa_operator_baseline": 1,
    "tpa_fetch_enabled": true,
    "tpa_fetch_url": "http://127.0.0.1:18702/scanner-bundle.json"
  }
}
```

Run one cycle and read the result:

```bash
./mcpproxy security bundle refresh --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data"
# Source: fetch (http://127.0.0.1)   Sequence: 3   Signature: verified
# Refresh: activated, next check …   Fetch host: 127.0.0.1
```

The status shows the host only, never the URL path. Then check the failure modes, each of which leaves the serving bundle untouched:

```bash
# unsigned: publish a sequence-4 bundle with no sidecar (the .sig 404s). A fetch is
# signed-only even with require_signed_bundle off.
jq --arg id "$KEY_ID" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
   '. + {sequence: "4", trust_epoch: 0, key_id: $id, generated_at: $now}' \
   internal/security/scanner/bundled/scanner-bundle.json > "$SCRATCH/mirror/scanner-bundle.json"
rm "$SCRATCH/mirror/scanner-bundle.json.sig"
./mcpproxy security bundle refresh …      # Last rejection: signature_required; sequence 3 keeps serving

# outage: stop the mirror
kill %1
./mcpproxy security bundle refresh …      # Refresh: fetch_unavailable (network_error); NOT a rejection

# no URL: fetch enabled with tpa_fetch_url "" makes zero requests
#   Refresh: disabled (no_url)   (and mcpproxy doctor says the fetch is enabled but inactive)
```

`mcpproxy security bundle reload` never makes a request, even with the fetch on: watch the mirror log while you run it. After a successful fetch, the new bundle's last-known-good copy survives a restart, and turning `tpa_fetch_enabled` off keeps it serving; `rollback --embedded --digest <embedded digest>` is the deliberate way back. An approved tool that matches a signature added by the fetched bundle is re-checked from its cached contract and shows a `dangerous` scan verdict on the server's review view, without being approved, revoked or quarantined (see `docs/features/security-quarantine.md`, "Re-protection after a bundle activates").

## 9. Contributor loop (User Story 2, not in the first PR)

```bash
go run ./cmd/tpa-db validate --bundle <path>   # schema + signatures[] integrity + licences
go run ./cmd/scan-eval --gate --bundle <path>  # recall ≥ 0.90 gated, hard-negative FP ≤ 0.05
```

`validate` enforces the [signatures[] contract](contracts/bundle-signatures-section.md) in both directions — every signature names a real rule, **and** every gating rule is named by exactly one signature. The second direction is the one that is easy to skip and the one that matters: without it a gating rule can ship with no provenance, no licence and no eval evidence.

`--gate` is the same code path activation runs (plan D8), so a bundle that passes here is one that will activate — the CI layer and the activation layer cannot drift apart.
