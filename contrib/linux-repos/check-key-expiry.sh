#!/usr/bin/env bash
# Emits a GitHub Actions ::warning:: annotation if the signing key expires
# within 60 days. Non-fatal — never exits non-zero.
#
# Expects GPG_KEY_ID to be imported into the current GNUPGHOME already
# (see import-key.sh).

set -uo pipefail

if [[ -z "${GPG_KEY_ID:-}" ]]; then
  echo "::warning::GPG_KEY_ID not set; skipping expiry check"
  exit 0
fi

# Parse the --with-colons output. The 'pub' line has the expiry timestamp
# in field 7 (seconds since epoch; empty means no expiry).
expiry=$(gpg --list-keys --with-colons "${GPG_KEY_ID}" 2>/dev/null \
  | awk -F: '/^pub:/ {print $7; exit}')

if [[ -z "${expiry}" ]]; then
  echo "Signing key ${GPG_KEY_ID} has no expiry — not emitting warning."
  exit 0
fi

now=$(date +%s)
days_left=$(( (expiry - now) / 86400 ))

if (( days_left < 0 )); then
  # Already expired — this is serious but we still don't fail the job here;
  # the sign step itself will fail, giving a more actionable error.
  echo "::error::Signing key ${GPG_KEY_ID} has EXPIRED (${days_left} days ago). Rotate immediately per docs/operations/linux-package-repos-infrastructure.md."
elif (( days_left < 60 )); then
  echo "::warning::Signing key ${GPG_KEY_ID} expires in ${days_left} days. Rotate soon — see docs/operations/linux-package-repos-infrastructure.md."
else
  echo "Signing key ${GPG_KEY_ID} expires in ${days_left} days — OK."
fi

exit 0
