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

# Import key unless already imported (e.g. during local dry-run against user's
# existing keyring).
if [[ -n "${PACKAGES_GPG_PRIVATE_KEY:-}" ]]; then
  "${here}/import-key.sh"
  # If import-key.sh wrote GNUPGHOME to $GITHUB_ENV, reload it for this process
  if [[ -n "${GITHUB_ENV:-}" && -f "${GITHUB_ENV}" ]]; then
    # shellcheck source=/dev/null
    eval "$(grep '^GNUPGHOME=' "${GITHUB_ENV}" | sed 's/^/export /')"
  fi
else
  echo "publish: PACKAGES_GPG_PRIVATE_KEY not set; assuming key is already in GNUPGHOME (local dry-run mode)"
fi

"${here}/check-key-expiry.sh" || true

"${here}/apt-publish.sh" "${ARTIFACTS_DIR}" ${DRY_RUN_FLAG:+"${DRY_RUN_FLAG}"}
"${here}/rpm-publish.sh" "${ARTIFACTS_DIR}" ${DRY_RUN_FLAG:+"${DRY_RUN_FLAG}"}

echo "publish: all repos updated"
