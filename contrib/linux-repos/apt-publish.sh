#!/usr/bin/env bash
# Publish / refresh the apt repository on R2.
#
# Usage:
#   apt-publish.sh ARTIFACTS_DIR [--dry-run]
#
# ARTIFACTS_DIR contains the newly-built *.deb files for this release.
#
# Flow:
#   1. sync down current bucket to a workdir
#   2. copy new .deb files into pool/main/m/mcpproxy/
#   3. prune all but the top RETAIN_N versions (semver-sorted)
#   4. regenerate Packages / Packages.gz / Release / Release.gpg / InRelease
#   5. sync up (unless --dry-run)
#
# Expects the following env vars:
#   APT_BUCKET        R2 bucket name (e.g. mcpproxy-apt)
#   APT_BASE_URL      (unused; kept for symmetry with rpm-publish.sh)
#   GPG_KEY_ID        fingerprint of the signing key (imported via import-key.sh)
#   PACKAGES_GPG_PASSPHRASE
#                     passphrase for the signing key
#   RETAIN_N          number of versions to retain (default 10)
#   AWS_ENDPOINT_URL  R2 S3 endpoint
#   AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY  R2 API creds

set -euo pipefail

ARTIFACTS_DIR="${1:-}"
DRY_RUN=false
if [[ "${2:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

if [[ -z "${ARTIFACTS_DIR}" || ! -d "${ARTIFACTS_DIR}" ]]; then
  echo "error: usage: apt-publish.sh ARTIFACTS_DIR [--dry-run]" >&2
  exit 1
fi

: "${APT_BUCKET:?APT_BUCKET env var required}"
: "${GPG_KEY_ID:?GPG_KEY_ID env var required}"
: "${PACKAGES_GPG_PASSPHRASE:?PACKAGES_GPG_PASSPHRASE env var required}"
RETAIN_N="${RETAIN_N:-10}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workdir=$(mktemp -d)
trap 'rm -rf "${workdir}"' EXIT

echo "apt-publish: workdir=${workdir}"

# 1. Sync current bucket state down
echo "[1/5] syncing bucket s3://${APT_BUCKET} -> ${workdir}"
if [[ "${DRY_RUN}" == "true" ]]; then
  echo "  dry-run: skipping sync-down"
else
  aws s3 sync "s3://${APT_BUCKET}/" "${workdir}/" --no-progress
fi

# Set up standard directory structure even if empty
mkdir -p "${workdir}/pool/main/m/mcpproxy"
mkdir -p "${workdir}/dists/stable/main/binary-amd64"
mkdir -p "${workdir}/dists/stable/main/binary-arm64"

# 2. Copy new .deb files into pool
echo "[2/5] adding new .deb files from ${ARTIFACTS_DIR}"
found=0
while IFS= read -r -d '' deb; do
  name=$(basename "${deb}")
  if [[ ! "${name}" =~ ^mcpproxy_[0-9]+\.[0-9]+\.[0-9]+_(amd64|arm64)\.deb$ ]]; then
    echo "  skip: ${name} does not match expected filename pattern"
    continue
  fi
  cp -f "${deb}" "${workdir}/pool/main/m/mcpproxy/${name}"
  echo "  added: ${name}"
  found=$(( found + 1 ))
done < <(find "${ARTIFACTS_DIR}" -maxdepth 2 -name '*.deb' -print0)

if (( found == 0 )); then
  echo "error: no .deb files matching mcpproxy_*_{amd64,arm64}.deb found in ${ARTIFACTS_DIR}" >&2
  exit 1
fi

# 3. Prune to top RETAIN_N versions
echo "[3/5] pruning to last ${RETAIN_N} versions"
cd "${workdir}/pool/main/m/mcpproxy"
if compgen -G "*.deb" > /dev/null; then
  mapfile -t versions < <(ls *.deb 2>/dev/null \
    | sed -E 's/^mcpproxy_([0-9]+\.[0-9]+\.[0-9]+)_(amd64|arm64)\.deb$/\1/' \
    | sort -V -u)
  total=${#versions[@]}
  echo "  total versions: ${total}"
  if (( total > RETAIN_N )); then
    prune_count=$(( total - RETAIN_N ))
    for v in "${versions[@]:0:prune_count}"; do
      echo "  pruning version ${v}"
      rm -f "mcpproxy_${v}_amd64.deb" "mcpproxy_${v}_arm64.deb"
    done
  else
    echo "  nothing to prune"
  fi
fi
cd "${workdir}"

# 4. Regenerate Packages / Release / sign
echo "[4/5] regenerating metadata and signing"

cd "${workdir}"
for arch in amd64 arm64; do
  apt-ftparchive --arch "${arch}" packages pool/main \
    > "dists/stable/main/binary-${arch}/Packages"
  gzip -9cf "dists/stable/main/binary-${arch}/Packages" \
    > "dists/stable/main/binary-${arch}/Packages.gz"
  wc -l "dists/stable/main/binary-${arch}/Packages" | sed 's/^/  /'
done

apt-ftparchive -c "${here}/apt-ftparchive.conf" release dists/stable \
  > dists/stable/Release

# Sign Release → Release.gpg (detached) and InRelease (clearsign)
rm -f dists/stable/Release.gpg dists/stable/InRelease

gpg --batch --yes --pinentry-mode loopback \
  --passphrase "${PACKAGES_GPG_PASSPHRASE}" \
  --local-user "${GPG_KEY_ID}" \
  --armor --detach-sign \
  --output dists/stable/Release.gpg \
  dists/stable/Release

gpg --batch --yes --pinentry-mode loopback \
  --passphrase "${PACKAGES_GPG_PASSPHRASE}" \
  --local-user "${GPG_KEY_ID}" \
  --clearsign \
  --output dists/stable/InRelease \
  dists/stable/Release

echo "  signed Release, Release.gpg, InRelease"

# Upload the public key alongside the metadata (idempotent; cheap).
gpg --export "${GPG_KEY_ID}" > "${workdir}/mcpproxy.gpg"
gpg --armor --export "${GPG_KEY_ID}" > "${workdir}/mcpproxy.gpg.asc"

# 5. Sync up
echo "[5/5] syncing ${workdir} -> s3://${APT_BUCKET}"
if [[ "${DRY_RUN}" == "true" ]]; then
  echo "  dry-run: final tree would be:"
  find "${workdir}" -type f | sort | sed "s|^${workdir}/|  |"
else
  # Metadata with short cache — must be under the time a client takes to
  # fetch Release + Packages.gz together, so that a user running `apt update`
  # never sees a Release referencing a stale Packages.gz.
  aws s3 sync "${workdir}/dists/" "s3://${APT_BUCKET}/dists/" \
    --cache-control "public, max-age=60, must-revalidate" --no-progress --delete
  # Pool artifacts are immutable
  aws s3 sync "${workdir}/pool/" "s3://${APT_BUCKET}/pool/" \
    --cache-control "public, max-age=31536000, immutable" --no-progress --delete
  # Public key
  aws s3 cp "${workdir}/mcpproxy.gpg" "s3://${APT_BUCKET}/mcpproxy.gpg" \
    --cache-control "public, max-age=86400" --no-progress
  aws s3 cp "${workdir}/mcpproxy.gpg.asc" "s3://${APT_BUCKET}/mcpproxy.gpg.asc" \
    --cache-control "public, max-age=86400" --no-progress
fi

echo "apt-publish: OK"
