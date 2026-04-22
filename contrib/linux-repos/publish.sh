#!/usr/bin/env bash
# Top-level orchestrator: import key → warn on expiry → apt publish → rpm publish.
#
# Usage:
#   publish.sh ARTIFACTS_DIR [--dry-run]

set -euo pipefail

ARTIFACTS_DIR="${1:-}"
DRY_RUN_FLAG="${2:-}"

if [[ -z "${ARTIFACTS_DIR}" ]]; then
  echo "error: usage: publish.sh ARTIFACTS_DIR [--dry-run]" >&2
  exit 1
fi

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# AWS CLI v2.23+ sends CRC32 checksum headers by default on PutObject, which
# Cloudflare R2 rejects with SignatureDoesNotMatch. Opt back into the pre-2.23
# behaviour: only include checksums when the service actually requires them.
export AWS_REQUEST_CHECKSUM_CALCULATION="when_required"
export AWS_RESPONSE_CHECKSUM_VALIDATION="when_required"

# Import key unless already imported (e.g. during local dry-run against user's
# existing keyring). We pin GNUPGHOME to a stable path so the child process
# that does the import and the later processes that do the signing all share
# the same keyring — without needing to shuttle env vars through $GITHUB_ENV.
if [[ -n "${PACKAGES_GPG_PRIVATE_KEY:-}" ]]; then
  if [[ -z "${GNUPGHOME:-}" ]]; then
    export GNUPGHOME="$(mktemp -d)"
  fi
  "${here}/import-key.sh"
else
  echo "publish: PACKAGES_GPG_PRIVATE_KEY not set; assuming key is already in GNUPGHOME (local dry-run mode)"
fi

"${here}/check-key-expiry.sh" || true

"${here}/apt-publish.sh" "${ARTIFACTS_DIR}" ${DRY_RUN_FLAG:+"${DRY_RUN_FLAG}"}
"${here}/rpm-publish.sh" "${ARTIFACTS_DIR}" ${DRY_RUN_FLAG:+"${DRY_RUN_FLAG}"}

echo "publish: all repos updated"
