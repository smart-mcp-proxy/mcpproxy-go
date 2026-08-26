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
go run ./cmd/tpa-db keygen --out "$SCRATCH/keys"
# writes keys/operator.key (private, 0600) and keys/operator.pub
# prints the key id: the lowercase-hex SHA-256 of the 32-byte raw public key
```

The **key id is a fingerprint of the key**, never a label you choose (FR-003). That is what makes "the manifest names one publisher, the bytes are signed by another" detectable rather than cosmetic.

## 2. Sign a bundle

```bash
go run ./cmd/tpa-db sign \
  --bundle internal/security/scanner/bundled/scanner-bundle.json \
  --key "$SCRATCH/keys/operator.key" \
  --sequence 1 \
  --epoch 0 \
  --out "$SCRATCH/scanner-bundle.json"
# writes scanner-bundle.json and scanner-bundle.json.sig
```

`sign` refuses a bundle that fails the eval gate — FR-016 means a bundle that cannot pass is never signed, so the gate runs here rather than only in CI.

Inspect the sidecar; it is one line, five keys, nothing else ([contract](contracts/sidecar-format.md)):

```bash
cat "$SCRATCH/scanner-bundle.json.sig" | jq .
```

## 3. Configure the operator authority and drop the bundle

An operator-signed bundle needs three config decisions, all of them **config-resident on purpose**: the config-write attacker is out of the threat model, the bundle-path attacker is in it.

```json
{
  "security": {
    "tpa_bundle_path": "<SCRATCH>/scanner-bundle.json",
    "tpa_active_authority": "operator",
    "tpa_operator_keys": ["<key id from step 1>"],
    "tpa_operator_baseline": 1,
    "tpa_operator_epoch": 0,
    "require_signed_bundle": true
  }
}
```

Declaring `tpa_operator_baseline` matters more than it looks. Without it the authority runs **explicitly degraded** rather than silently trusted — with no baseline there is no anti-downgrade at all after a data-directory wipe, and an attacker who can write the bundle path could replay any older genuinely operator-signed bundle at full coverage (FR-009b).

Start the proxy; the file watcher picks the bundle up without a restart.

```bash
./mcpproxy serve --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" --log-level=debug
```

## 4. Verify it activated

```bash
./mcpproxy security bundle -o json --config "$SCRATCH/config.json" --data-dir "$SCRATCH/data" | jq .
```

Expect `signature_verified: true`, `authority: "operator"`, `trust_epoch: 0`, `sequence: "1"`, a `full_digest`, `coverage_degraded: false`.

**If `coverage_degraded` is true, read `degraded_reason` before anything else** — it distinguishes "you forgot the baseline" (`operator_authority_unbaselined`) from "the bundle is unsigned" (`unsigned_external_active`) from "we fell back to the embedded default" (`embedded_below_watermark`). A degraded install has `scan`-mode auto-approval suspended, which is otherwise invisible.

## 5. Watch a downgrade be refused

Sign the same corpus at a lower sequence and drop it:

```bash
go run ./cmd/tpa-db sign --bundle … --sequence 1 --epoch 0 --out "$SCRATCH/old.json"
cp "$SCRATCH/old.json"* "$SCRATCH/"   # overwrite the configured path
```

The sequence-1 artifact is now at or below the watermark, so it is refused and the current bundle keeps serving:

```bash
./mcpproxy security bundle -o json … | jq '.last_rejection'
# { "reason": "downgrade", "at": "…" }
```

Nothing about this is fatal — every rejection keeps last-known-good. That is the point: a refused candidate must never leave the scanner with an empty or partial rule set.

## 6. Roll back — by digest, not by path

Read the history to learn the digest you want:

```bash
./mcpproxy security bundle history -o json … | jq '.[] | {sequence, full_digest, activated_at}'
```

Then roll back naming **the full digest**:

```bash
./mcpproxy security bundle rollback \
  --path "$SCRATCH/old.json" \
  --digest <full sha256 from the history> …
```

The path is a *hint about where to find those bytes*; the digest is the authorization. Rollback is the one path that deliberately bypasses the watermark, so identifying its target by an attacker-writable path would hand the attacker the choice of what the bypass lands on. Supply the wrong digest and it is refused `rollback_target_mismatch` — try it, it is one command:

```bash
./mcpproxy security bundle rollback --path "$SCRATCH/old.json" --digest 0000…  # refused
```

**The 12-hex short fingerprint is not accepted here.** It is 48 bits — fine as a human label, not as authorization for a watermark bypass where the attacker chooses the substituted bytes.

After a rollback the artifact is **pinned** and the release you rolled away from is **deny-listed**, so the next refresh cycle cannot re-activate the very thing you just rejected. Clear the pin to return to normal watermark-governed activation:

```bash
./mcpproxy security bundle unpin …
```

## 7. Air-gapped check (SC-003)

Steps 1–6 with the network unavailable behave identically. That is the criterion — the file-drop path is not a degraded mode, it is the primary one, and the fetch path adds convenience rather than capability.

## 8. Contributor loop

```bash
go run ./cmd/tpa-db validate --bundle <path>   # schema + signatures[] integrity + licences
go run ./cmd/scan-eval --gate --bundle <path>  # recall ≥ 0.90 gated, hard-negative FP ≤ 0.05
```

`validate` enforces the [signatures[] contract](contracts/bundle-signatures-section.md) in both directions — every signature names a real rule, **and** every gating rule is named by exactly one signature. The second direction is the one that is easy to skip and the one that matters: without it a gating rule can ship with no provenance, no licence and no eval evidence.

`--gate` is the same code path activation runs (plan D8), so a bundle that passes here is one that will activate — the CI layer and the activation layer cannot drift apart.
