# Quickstart — Initial Setup and First Publish

One-time setup steps to go from zero → working apt.mcpproxy.app and rpm.mcpproxy.app. Executed by the maintainer on their workstation, once, before the first CI run.

## Prerequisites

- `wrangler` authenticated to the Cloudflare account that owns `mcpproxy.app` (`wrangler whoami` works).
- `gpg` installed (`gpg --version`).
- `gh` CLI authenticated to the `smart-mcp-proxy/mcpproxy-go` repository with `repo` scope.
- The two R2 buckets do not yet exist and the two subdomains have no DNS records.

## Step 1 — Generate the GPG signing key

```bash
# Generate without prompts
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
Passphrase: <generated, capture into 1Password>
%commit
%echo done
EOF

gpg --batch --generate-key /tmp/gpg-batch.txt
shred -u /tmp/gpg-batch.txt

# Get the fingerprint
FPR=$(gpg --list-keys --with-colons mcpproxy-packages@mcpproxy.app | awk -F: '/^fpr:/ {print $10; exit}')
echo "Fingerprint: ${FPR}"
```

Record the fingerprint; it will be pasted into the website install page and this doc.

## Step 2 — Export keys and write the backup file

```bash
# Public key into the repo
gpg --armor --export "${FPR}" > ~/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc

# Private key into the local backup file (outside any git repo)
{
  cat <<EOF
MCPProxy Linux Package Signing Key — BACKUP
============================================

Key ID:        ${FPR}
Created:       $(date -u +%Y-%m-%d)
Expires:       $(date -u -v+5y +%Y-%m-%d 2>/dev/null || date -u -d '+5 years' +%Y-%m-%d)
UID:           MCPProxy Packages <mcpproxy-packages@mcpproxy.app>
Passphrase:    stored separately in 1Password (item: "MCPProxy Packages GPG")

This is the private GPG key used to sign apt.mcpproxy.app and
rpm.mcpproxy.app repo metadata. Keep this file safe and outside any git repo.

## How to use this file

### Re-import locally (for manual resigning / disaster recovery):
  gpg --import < ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt

### Refresh the GitHub Actions secret from this file:
  gh secret set PACKAGES_GPG_PRIVATE_KEY \\
    --repo smart-mcp-proxy/mcpproxy-go < ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt

### Rotate (annually, or on suspected leak):
  1. gpg --full-generate-key (RSA 4096, 5y expiry, UID above)
  2. gpg --armor --export-secret-keys <newid> > ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
  3. gpg --armor --export <newid> > ~/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc
  4. gh secret set PACKAGES_GPG_PRIVATE_KEY ... (as above)
  5. Commit the new public key and tag a release → CI republishes to R2.
  6. Announce rotation in release notes and on the install page.

-----BEGIN PGP PRIVATE KEY BLOCK-----
EOF
  gpg --armor --export-secret-keys "${FPR}" | sed -n '/-----BEGIN/,/-----END/p' | tail -n +2
} > ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt

chmod 600 ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
```

## Step 3 — Create R2 buckets

```bash
cd ~/repos/mcpproxy-go
wrangler r2 bucket create mcpproxy-apt
wrangler r2 bucket create mcpproxy-rpm
```

## Step 4 — Bind custom domains (Cloudflare dashboard)

1. Open https://dash.cloudflare.com → R2 → Buckets
2. For `mcpproxy-apt`: Settings → Custom Domains → Connect Domain → `apt.mcpproxy.app`
3. For `mcpproxy-rpm`: same flow → `rpm.mcpproxy.app`
4. Cloudflare automatically creates the CNAME records and provisions HTTPS.

(If the R2 CLI is updated to expose domain binding as a non-interactive command, move this step to wrangler.)

## Step 5 — Upload the public key (once)

```bash
aws configure set default.region auto

export AWS_ENDPOINT_URL="https://${CF_ACCOUNT_ID}.r2.cloudflarestorage.com"
export AWS_ACCESS_KEY_ID=...   # from an R2 token scoped to these buckets
export AWS_SECRET_ACCESS_KEY=...

# Binary form (what apt expects by default for keyring files)
gpg --export "${FPR}" > /tmp/mcpproxy.gpg
aws s3 cp /tmp/mcpproxy.gpg s3://mcpproxy-apt/mcpproxy.gpg \
  --cache-control "public, max-age=86400"
aws s3 cp /tmp/mcpproxy.gpg s3://mcpproxy-rpm/mcpproxy.gpg \
  --cache-control "public, max-age=86400"

# ASCII-armored form
aws s3 cp ~/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc \
  s3://mcpproxy-apt/mcpproxy.gpg.asc --cache-control "public, max-age=86400"
aws s3 cp ~/repos/mcpproxy-go/contrib/signing/mcpproxy-packages.asc \
  s3://mcpproxy-rpm/mcpproxy.gpg.asc --cache-control "public, max-age=86400"
```

## Step 6 — Register GitHub Actions secrets and variables

```bash
# Private key + passphrase
gh secret set PACKAGES_GPG_PRIVATE_KEY --repo smart-mcp-proxy/mcpproxy-go \
  < ~/repos/PACKAGES_GPG_PRIVATE_KEY.txt
echo -n "${GPG_PASSPHRASE}" \
  | gh secret set PACKAGES_GPG_PASSPHRASE --repo smart-mcp-proxy/mcpproxy-go

# R2 credentials
echo -n "${CF_ACCOUNT_ID}" | gh secret set R2_ACCOUNT_ID --repo smart-mcp-proxy/mcpproxy-go
echo -n "${R2_ACCESS_KEY_ID}" | gh secret set R2_ACCESS_KEY_ID --repo smart-mcp-proxy/mcpproxy-go
echo -n "${R2_SECRET_ACCESS_KEY}" | gh secret set R2_SECRET_ACCESS_KEY --repo smart-mcp-proxy/mcpproxy-go

# Non-secret variable: the GPG key fingerprint, so the workflow can pick the right key
gh variable set PACKAGES_GPG_KEY_ID --repo smart-mcp-proxy/mcpproxy-go --body "${FPR}"
```

## Step 7 — First publish

Push a `v*` tag. The workflow's `publish-linux-repos` job will:

1. Download `.deb`/`.rpm` artifacts from the `build` job.
2. Sync the buckets down (empty on first run).
3. Add artifacts, run `apt-ftparchive` / `createrepo_c`, sign with the imported key.
4. Sync back up.
5. Run smoke-test install in Debian + Fedora containers.

## Step 8 — Smoke test manually (optional)

On any Linux workstation or in a fresh container:

```bash
docker run --rm -it debian:stable-slim bash -c '
  apt-get update && apt-get install -y curl ca-certificates
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://apt.mcpproxy.app/mcpproxy.gpg -o /etc/apt/keyrings/mcpproxy.gpg
  echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/mcpproxy.gpg] https://apt.mcpproxy.app stable main" \
    > /etc/apt/sources.list.d/mcpproxy.list
  apt-get update
  apt-get install -y mcpproxy
  mcpproxy --version
'
```
