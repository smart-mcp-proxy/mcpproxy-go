# Contract: `publish-linux-repos` CI Job

## Location
`.github/workflows/release.yml`, new job appended.

## Trigger
Same as the parent workflow: `on: push: tags: ["v*"]`, plus `workflow_dispatch`.

## Dependencies (`needs`)
`[build]` — requires the `.deb` and `.rpm` artifacts built earlier in the workflow.

## Guard
```yaml
if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, '-')
```

Rationale: `!contains(github.ref, '-')` filters out tags like `v1.0.0-rc1`. Stable-only channel (FR-022).

## Inputs (reads)

**Secrets (`secrets.*`)**:
- `PACKAGES_GPG_PRIVATE_KEY` — ASCII-armored private key
- `PACKAGES_GPG_PASSPHRASE` — passphrase for the private key
- `R2_ACCOUNT_ID` — Cloudflare account ID (appears in the R2 S3 endpoint URL)
- `R2_ACCESS_KEY_ID` — R2 API token access key (scoped to these two buckets)
- `R2_SECRET_ACCESS_KEY` — R2 API token secret

**Environment variables (`env:`)**:
- `APT_BUCKET=mcpproxy-apt`
- `RPM_BUCKET=mcpproxy-rpm`
- `APT_BASE_URL=https://apt.mcpproxy.app`
- `RPM_BASE_URL=https://rpm.mcpproxy.app`
- `GPG_KEY_ID` — long-form key ID of the signing key (used to select which key to sign with)
- `RETAIN_N=10`

**Artifacts (downloaded)**:
- The four `.deb`/`.rpm` files produced by the `build` job.

## Outputs (writes)

**R2 bucket `mcpproxy-apt`**:
- New `.deb` files added to `pool/main/m/mcpproxy/`
- Stale `.deb` files for the `N > 10` oldest versions deleted
- Regenerated `dists/stable/main/binary-{amd64,arm64}/Packages{,.gz}`
- Regenerated `dists/stable/Release`, `Release.gpg`, `InRelease`
- Updated `mcpproxy.gpg` (only changed if the key itself rotated)

**R2 bucket `mcpproxy-rpm`**:
- New `.rpm` files added to `x86_64/` or `aarch64/`
- Stale `.rpm` files for the `N > 10` oldest versions deleted, per-arch
- Regenerated `{x86_64,aarch64}/repodata/*` via `createrepo_c`
- Regenerated `{x86_64,aarch64}/repodata/repomd.xml.asc` (detached signature)
- Updated `mcpproxy.repo`, `mcpproxy.gpg`

**Workflow outcome**:
- Job status: `success` iff (sync + sign + smoke-test in Debian + smoke-test in Fedora) all pass
- Job status: `failure` otherwise; the release tag publishes to GitHub normally (that happens in an earlier job) but the apt/yum repos are not updated

## Step-by-step contract

```yaml
publish-linux-repos:
  needs: [build]
  if: startsWith(github.ref, 'refs/tags/v') && !contains(github.ref, '-')
  runs-on: ubuntu-latest
  environment: production

  env:
    APT_BUCKET: mcpproxy-apt
    RPM_BUCKET: mcpproxy-rpm
    APT_BASE_URL: https://apt.mcpproxy.app
    RPM_BASE_URL: https://rpm.mcpproxy.app
    GPG_KEY_ID: ${{ vars.PACKAGES_GPG_KEY_ID }}
    RETAIN_N: "10"
    AWS_ENDPOINT_URL: https://${{ secrets.R2_ACCOUNT_ID }}.r2.cloudflarestorage.com
    AWS_DEFAULT_REGION: auto
    AWS_ACCESS_KEY_ID: ${{ secrets.R2_ACCESS_KEY_ID }}
    AWS_SECRET_ACCESS_KEY: ${{ secrets.R2_SECRET_ACCESS_KEY }}

  steps:
    - name: Checkout (for helper scripts + public key)
      uses: actions/checkout@v4

    - name: Install repo tooling
      run: sudo apt-get update && sudo apt-get install -y apt-utils createrepo-c gnupg

    - name: Download package artifacts
      uses: actions/download-artifact@v4
      with:
        path: release-artifacts
        pattern: linux-packages-*
        merge-multiple: true

    - name: Import GPG signing key
      env:
        PACKAGES_GPG_PRIVATE_KEY: ${{ secrets.PACKAGES_GPG_PRIVATE_KEY }}
        PACKAGES_GPG_PASSPHRASE: ${{ secrets.PACKAGES_GPG_PASSPHRASE }}
      run: contrib/linux-repos/import-key.sh

    - name: Warn on near-expiry signing key
      run: contrib/linux-repos/check-key-expiry.sh   # non-fatal warning annotation

    - name: Publish apt repo
      run: contrib/linux-repos/apt-publish.sh release-artifacts

    - name: Publish rpm repo
      run: contrib/linux-repos/rpm-publish.sh release-artifacts

    - name: Smoke-test install (Debian)
      run: contrib/linux-repos/smoke-test-debian.sh "${GITHUB_REF_NAME#v}"

    - name: Smoke-test install (Fedora)
      run: contrib/linux-repos/smoke-test-fedora.sh "${GITHUB_REF_NAME#v}"
```

## Idempotency guarantee

Running the job twice for the same tag is safe. Because the publish scripts are stateless (they re-scan the pool each time) and `apt-ftparchive` / `createrepo_c` produce the same output given the same input, a re-run converges to the same final state.

## Blast-radius containment

- On failure, the published `v*` tag and GitHub Release are NOT rolled back — those are produced by earlier jobs. Only the apt/yum repos may be in an in-between state.
- If the job fails partway, a human can re-trigger the workflow (`workflow_dispatch` with the same tag) and the stateless publish scripts will clean up.
- If a genuinely bad artifact reaches the repo, the operations runbook (`docs/operations/linux-package-repos-infrastructure.md`) documents the manual purge procedure.
