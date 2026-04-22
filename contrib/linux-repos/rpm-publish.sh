#!/usr/bin/env bash
# Publish / refresh the rpm repository on R2.
#
# Usage:
#   rpm-publish.sh ARTIFACTS_DIR [--dry-run]
#
# Flow per arch (x86_64, aarch64):
#   1. sync down current bucket to a workdir
#   2. copy new .rpm into {arch}/
#   3. prune all but the top RETAIN_N versions
#   4. createrepo_c → regenerate repodata/
#   5. sign repomd.xml
#   6. upload mcpproxy.repo, public key
#   7. sync up (unless --dry-run)

set -euo pipefail

ARTIFACTS_DIR="${1:-}"
DRY_RUN=false
if [[ "${2:-}" == "--dry-run" ]]; then
  DRY_RUN=true
fi

if [[ -z "${ARTIFACTS_DIR}" || ! -d "${ARTIFACTS_DIR}" ]]; then
  echo "error: usage: rpm-publish.sh ARTIFACTS_DIR [--dry-run]" >&2
  exit 1
fi

: "${RPM_BUCKET:?RPM_BUCKET env var required}"
: "${GPG_KEY_ID:?GPG_KEY_ID env var required}"
: "${PACKAGES_GPG_PASSPHRASE:?PACKAGES_GPG_PASSPHRASE env var required}"
RETAIN_N="${RETAIN_N:-10}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
workdir=$(mktemp -d)
trap 'rm -rf "${workdir}"' EXIT

echo "rpm-publish: workdir=${workdir}"

# 1. Sync current bucket state down
echo "[1/7] syncing bucket s3://${RPM_BUCKET} -> ${workdir}"
if [[ "${DRY_RUN}" == "true" ]]; then
  echo "  dry-run: skipping sync-down"
else
  aws s3 sync "s3://${RPM_BUCKET}/" "${workdir}/" --no-progress
fi
mkdir -p "${workdir}/x86_64" "${workdir}/aarch64"

# 2. Copy new .rpm files
echo "[2/7] adding new .rpm files from ${ARTIFACTS_DIR}"
found=0
while IFS= read -r -d '' rpm; do
  name=$(basename "${rpm}")
  if [[ "${name}" =~ ^mcpproxy-[0-9]+\.[0-9]+\.[0-9]+-[0-9]+\.x86_64\.rpm$ ]]; then
    cp -f "${rpm}" "${workdir}/x86_64/${name}"
    echo "  added x86_64: ${name}"
    found=$(( found + 1 ))
  elif [[ "${name}" =~ ^mcpproxy-[0-9]+\.[0-9]+\.[0-9]+-[0-9]+\.aarch64\.rpm$ ]]; then
    cp -f "${rpm}" "${workdir}/aarch64/${name}"
    echo "  added aarch64: ${name}"
    found=$(( found + 1 ))
  else
    echo "  skip: ${name} does not match expected filename pattern"
  fi
done < <(find "${ARTIFACTS_DIR}" -maxdepth 2 -name '*.rpm' -print0)

if (( found == 0 )); then
  echo "error: no .rpm files matching mcpproxy-*-*.{x86_64,aarch64}.rpm found in ${ARTIFACTS_DIR}" >&2
  exit 1
fi

# 2b. Sign newly-added RPMs. dnf with gpgcheck=1 verifies each package's
# embedded signature — not just the repo metadata signature — so the RPM files
# must be signed before createrepo_c is run.
echo "[2b/7] signing new .rpm packages"

# rpmsign reads the signing key via rpm macros; set them up against our GNUPGHOME.
cat > "${HOME}/.rpmmacros" <<EOF
%_signature gpg
%_gpg_name ${GPG_KEY_ID}
%_gpg_path ${GNUPGHOME}
%__gpg_sign_cmd /usr/bin/gpg --batch --yes --pinentry-mode loopback --passphrase ${PACKAGES_GPG_PASSPHRASE} --no-armor --no-secmem-warning -u %{_gpg_name} -sbo %{__signature_filename} --digest-algo sha256 %{__plaintext_filename}
EOF

for arch in x86_64 aarch64; do
  for rpm in "${workdir}/${arch}/"mcpproxy-*-1.${arch}.rpm; do
    [[ -e "${rpm}" ]] || continue
    # Skip if already signed (idempotent re-runs)
    if rpm --checksig "${rpm}" 2>&1 | grep -q "pgp"; then
      echo "  already signed: $(basename "${rpm}")"
      continue
    fi
    rpmsign --addsign "${rpm}" > /dev/null 2>&1 || {
      echo "error: rpmsign failed for ${rpm}" >&2
      exit 1
    }
    echo "  signed: $(basename "${rpm}")"
  done
done

rm -f "${HOME}/.rpmmacros"

# 3. Prune per-arch to top RETAIN_N versions
for arch in x86_64 aarch64; do
  echo "[3/7] pruning ${arch} to last ${RETAIN_N} versions"
  cd "${workdir}/${arch}"
  if compgen -G "*.rpm" > /dev/null; then
    mapfile -t versions < <(ls *.rpm 2>/dev/null \
      | sed -E "s/^mcpproxy-([0-9]+\.[0-9]+\.[0-9]+)-[0-9]+\.${arch}\.rpm\$/\\1/" \
      | sort -V -u)
    total=${#versions[@]}
    echo "  total versions: ${total}"
    if (( total > RETAIN_N )); then
      prune_count=$(( total - RETAIN_N ))
      for v in "${versions[@]:0:prune_count}"; do
        echo "  pruning version ${v}"
        rm -f mcpproxy-${v}-*.${arch}.rpm
      done
    fi
  fi
  cd "${workdir}"
done

# 4. createrepo_c per arch
for arch in x86_64 aarch64; do
  echo "[4/7] createrepo_c ${arch}"
  # --update would be faster but we want stateless; full regen is safe here
  # since per-arch content is only RPM + repodata.
  rm -rf "${workdir}/${arch}/repodata"
  createrepo_c --general-compress-type=gz "${workdir}/${arch}/" > /dev/null
done

# 5. Sign repomd.xml per arch
for arch in x86_64 aarch64; do
  echo "[5/7] signing ${arch}/repodata/repomd.xml"
  rm -f "${workdir}/${arch}/repodata/repomd.xml.asc"
  gpg --batch --yes --pinentry-mode loopback \
    --passphrase "${PACKAGES_GPG_PASSPHRASE}" \
    --local-user "${GPG_KEY_ID}" \
    --armor --detach-sign \
    --output "${workdir}/${arch}/repodata/repomd.xml.asc" \
    "${workdir}/${arch}/repodata/repomd.xml"
done

# 6. Upload mcpproxy.repo + public key
echo "[6/7] writing .repo file + public key"
cp -f "${here}/mcpproxy.repo.template" "${workdir}/mcpproxy.repo"
gpg --export "${GPG_KEY_ID}" > "${workdir}/mcpproxy.gpg"
gpg --armor --export "${GPG_KEY_ID}" > "${workdir}/mcpproxy.gpg.asc"

# 7. Sync up
echo "[7/7] syncing ${workdir} -> s3://${RPM_BUCKET}"
if [[ "${DRY_RUN}" == "true" ]]; then
  echo "  dry-run: final tree would be:"
  find "${workdir}" -type f | sort | sed "s|^${workdir}/|  |"
else
  # Per-arch content: metadata short cache, rpms immutable
  for arch in x86_64 aarch64; do
    aws s3 sync "${workdir}/${arch}/repodata/" "s3://${RPM_BUCKET}/${arch}/repodata/" \
      --cache-control "public, max-age=60, must-revalidate" --no-progress --delete
    # rpms — upload individually with immutable cache (sync --cache-control is per-run)
    for f in "${workdir}/${arch}/"*.rpm; do
      [[ -e "${f}" ]] || continue
      aws s3 cp "${f}" "s3://${RPM_BUCKET}/${arch}/$(basename "${f}")" \
        --cache-control "public, max-age=31536000, immutable" --no-progress
    done
    # Delete rpms that no longer exist locally (after pruning)
    aws s3 ls "s3://${RPM_BUCKET}/${arch}/" --no-paginate \
      | awk '{print $4}' \
      | grep -E '\.rpm$' \
      | while read -r remote; do
        [[ -z "${remote}" ]] && continue
        if [[ ! -f "${workdir}/${arch}/${remote}" ]]; then
          echo "  deleting stale ${arch}/${remote}"
          aws s3 rm "s3://${RPM_BUCKET}/${arch}/${remote}" --no-progress
        fi
      done
  done
  aws s3 cp "${workdir}/mcpproxy.repo" "s3://${RPM_BUCKET}/mcpproxy.repo" \
    --cache-control "public, max-age=3600" --no-progress
  aws s3 cp "${workdir}/mcpproxy.gpg" "s3://${RPM_BUCKET}/mcpproxy.gpg" \
    --cache-control "public, max-age=86400" --no-progress
  aws s3 cp "${workdir}/mcpproxy.gpg.asc" "s3://${RPM_BUCKET}/mcpproxy.gpg.asc" \
    --cache-control "public, max-age=86400" --no-progress
fi

echo "rpm-publish: OK"
