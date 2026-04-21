---
id: linux-package-repos-infrastructure
title: Linux Package Repositories — Operations
sidebar_label: Linux package repos — ops
description: Maintainer runbook for apt.mcpproxy.app / rpm.mcpproxy.app — infra layout, key rotation, manual republish, bad-version purge.
---

# Linux Package Repositories — Operations

Maintainer runbook for the apt and yum repositories at `apt.mcpproxy.app` / `rpm.mcpproxy.app`.

> **User-facing details** live in [Linux Package Repositories](../features/linux-package-repos.md). This document covers the operator perspective only.

## Infrastructure inventory

**Cloudflare R2 buckets** (Eastern Europe region, Standard storage class):
- `mcpproxy-apt` — bound to custom domain `apt.mcpproxy.app`
- `mcpproxy-rpm` — bound to custom domain `rpm.mcpproxy.app`

**Cloudflare R2 API token** (dashboard → R2 → API tokens): `MCPProxy Packages CI`, Object Read & Write, scoped to the two buckets, no expiry.

**GitHub Actions secrets** in `smart-mcp-proxy/mcpproxy-go`:
- `PACKAGES_GPG_PRIVATE_KEY` — ASCII-armored GPG private key
- `PACKAGES_GPG_PASSPHRASE` — passphrase for that key
- `R2_ACCOUNT_ID` — Cloudflare account ID (appears in the R2 S3 endpoint URL)
- `R2_ACCESS_KEY_ID` — R2 API token access key
- `R2_SECRET_ACCESS_KEY` — R2 API token secret

**GitHub Actions variable**:
- `PACKAGES_GPG_KEY_ID` — full fingerprint of the current signing key

**Local-only backup** on maintainer workstation:
- `~/repos/PACKAGES_GPG_PRIVATE_KEY.txt` (mode 0600, outside any git repo) — ASCII-armored private key + usage instructions.

**CI job**: `publish-linux-repos` in `.github/workflows/release.yml`, triggered on every `v*` tag that does not contain a `-` (stable channel only).

**Helper scripts**: `contrib/linux-repos/*.sh` — orchestrator (`publish.sh`), per-format publishers (`apt-publish.sh`, `rpm-publish.sh`), smoke tests (`smoke-test-debian.sh`, `smoke-test-fedora.sh`), key import (`import-key.sh`), expiry check (`check-key-expiry.sh`).

## GPG key rotation procedure

Rotate the signing key annually (calendar reminder), or immediately on any hint of compromise. Budget: 30 minutes end-to-end.

### 1. Generate the new key

```bash
# On maintainer workstation — same recipe as initial setup
cat > /tmp/gpg-batch.txt <<'EOF'
%echo Generating MCPProxy package signing key
Key-Type: RSA
Key-Length: 4096
Key-Usage: sign
Subkey-Type: RSA
Subkey-Length: 4096
Subkey-Usage: encrypt
Name-Real: MCPProxy Packages
Name-Email: mcpproxy-packages@mcpproxy.app
Name-Comment: Linux repository signing key
Expire-Date: 5y
Passphrase: <generate fresh, capture into 1Password>
%commit
%echo done
EOF

gpg --batch --generate-key /tmp/gpg-batch.txt
shred -u /tmp/gpg-batch.txt
NEW_FPR=$(gpg --list-keys --with-colons 'MCPProxy Packages' \
  | awk -F: '/^fpr:/ {print $10}' | tail -1)
echo "New fingerprint: ${NEW_FPR}"
```

### 2. Refresh local backup

```bash
# Export the new private key (and metadata) into the backup file
gpg --armor --export-secret-keys "${NEW_FPR}" > ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
# Edit the header lines (Key ID / Created / Expires / Passphrase) by hand
chmod 600 ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
```

### 3. Update the public-key file in the repo

```bash
gpg --armor --export "${NEW_FPR}" \
  > ~/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc
```

### 4. Update GitHub Actions secrets and variable

```bash
gh secret set PACKAGES_GPG_PRIVATE_KEY \
  --repo smart-mcp-proxy/mcpproxy-go < ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
echo -n "<new passphrase>" | gh secret set PACKAGES_GPG_PASSPHRASE \
  --repo smart-mcp-proxy/mcpproxy-go
gh variable set PACKAGES_GPG_KEY_ID \
  --repo smart-mcp-proxy/mcpproxy-go --body "${NEW_FPR}"
```

### 5. Commit the new public key and tag a release

```bash
cd ~/repos/mcpproxy-go
git add contrib/signing/mcpproxy-packages.asc
git commit -m "chore(signing): rotate linux package signing key to <short-id>"
git push
git tag -a vX.Y.Z -m "Release vX.Y.Z"
git push origin vX.Y.Z
```

The next workflow run regenerates all signed metadata using the new key and republishes the public key to both buckets.

### 6. Announce rotation

- Add a note to the release notes (in the release PR or GitHub release body).
- Update the fingerprint shown in `docs/features/linux-package-repos.md` and `README.md`.
- Update `docs/getting-started/installation.md`.
- Tell users they may need to re-import the key after the rotation (`curl -fsSL .../mcpproxy.gpg | sudo tee /etc/apt/keyrings/mcpproxy.gpg > /dev/null` and equivalent for dnf).

### 7. Revoke the old key (optional)

Only if you suspect compromise. Generate a revocation certificate and publish it to the same URL as the public key. Most users don't check revocations for package-signing keys; the stronger lever is rolling the GitHub Actions secret (done in step 4), which invalidates any attacker-held copy.

## Manual republish of a single release

If the `publish-linux-repos` job failed for a specific tag and you need to re-run it without re-tagging:

1. Go to the Actions tab in the repository
2. Pick the failed workflow run for the tag
3. Click "Re-run failed jobs" — only the `publish-linux-repos` job re-runs
4. Scripts are stateless; a retry converges to the correct final state

If the workflow_dispatch trigger is enabled, you can also dispatch manually with the tag as input.

For a completely offline republish from the maintainer workstation (last resort):

```bash
export PACKAGES_GPG_PRIVATE_KEY=$(cat ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt)
export PACKAGES_GPG_PASSPHRASE='<passphrase>'
export GPG_KEY_ID='3B6FA1AD5D5359DA51F18DDCE1B59B9BA1CB8A3B'
export APT_BUCKET=mcpproxy-apt
export RPM_BUCKET=mcpproxy-rpm
export APT_BASE_URL=https://apt.mcpproxy.app
export RPM_BASE_URL=https://rpm.mcpproxy.app
export AWS_ACCESS_KEY_ID=<from secrets>
export AWS_SECRET_ACCESS_KEY=<from secrets>
export AWS_ENDPOINT_URL=https://<account>.r2.cloudflarestorage.com
export AWS_DEFAULT_REGION=auto
# Download the .deb/.rpm from the GitHub release into ./release-artifacts/
./contrib/linux-repos/publish.sh ./release-artifacts/
```

## Purge a bad release

If a published version needs to be pulled (security regression, broken binary, etc.):

```bash
# From the workstation, with R2 creds loaded:
export BAD_VERSION=0.24.7
export AWS_ENDPOINT_URL=https://<account>.r2.cloudflarestorage.com
export AWS_DEFAULT_REGION=auto
export AWS_ACCESS_KEY_ID=<from secrets>
export AWS_SECRET_ACCESS_KEY=<from secrets>

# Delete pool artifacts from apt bucket
aws s3 rm "s3://mcpproxy-apt/pool/main/m/mcpproxy/mcpproxy_${BAD_VERSION}_amd64.deb"
aws s3 rm "s3://mcpproxy-apt/pool/main/m/mcpproxy/mcpproxy_${BAD_VERSION}_arm64.deb"

# Delete rpms from rpm bucket
aws s3 rm "s3://mcpproxy-rpm/x86_64/mcpproxy-${BAD_VERSION}-1.x86_64.rpm"
aws s3 rm "s3://mcpproxy-rpm/aarch64/mcpproxy-${BAD_VERSION}-1.aarch64.rpm"

# Re-run the publish job (which will regenerate metadata without the bad files).
# Simplest way: tag a new release, which naturally pushes the bad version out anyway.
```

If you cannot cut a new release immediately, you can force a metadata-only refresh by re-running the most recent successful `publish-linux-repos` workflow.

## Cost model (Cloudflare R2)

The current usage fits the free tier:
- Storage: ~200 MB at 10 releases × 4 artifacts × ~16 MB (limit 10 GB)
- Class A (writes): ~30–50/run × 1 run/release × ~10 releases/month ≈ 300–500/month (limit 1M/month)
- Class B (reads): dominated by `apt update` / `dnf makecache`; capacity for ~300k Linux users/day before hitting the 10M/month limit

Egress is free on R2. No billing expected under normal usage.

## Related

- User-facing: [Linux Package Repositories](../features/linux-package-repos.md)
- Feature spec: [`specs/043-linux-package-repos/`](https://github.com/smart-mcp-proxy/mcpproxy-go/tree/main/specs/043-linux-package-repos) (internal reference)
