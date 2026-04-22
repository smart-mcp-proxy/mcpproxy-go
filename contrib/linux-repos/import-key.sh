#!/usr/bin/env bash
# Import the MCPProxy package signing key from PACKAGES_GPG_PRIVATE_KEY env
# var into a scratch GNUPGHOME, and preload the passphrase so later signing
# ops run non-interactively.
#
# Outputs (to the GitHub Actions environment, via $GITHUB_ENV):
#   GNUPGHOME — scratch directory containing the imported key
#
# Expects:
#   PACKAGES_GPG_PRIVATE_KEY   ASCII-armored private key
#   PACKAGES_GPG_PASSPHRASE    passphrase for that key
#   GPG_KEY_ID                 full fingerprint of the key to import

set -euo pipefail

if [[ -z "${PACKAGES_GPG_PRIVATE_KEY:-}" ]]; then
  echo "error: PACKAGES_GPG_PRIVATE_KEY env var is empty" >&2
  exit 1
fi
if [[ -z "${PACKAGES_GPG_PASSPHRASE:-}" ]]; then
  echo "error: PACKAGES_GPG_PASSPHRASE env var is empty" >&2
  exit 1
fi
if [[ -z "${GPG_KEY_ID:-}" ]]; then
  echo "error: GPG_KEY_ID env var is empty" >&2
  exit 1
fi

# Honor GNUPGHOME if caller set one (e.g. publish.sh passes a stable path so
# subsequent sub-processes in the same pipeline can find the key). Otherwise
# create a scratch directory.
if [[ -z "${GNUPGHOME:-}" ]]; then
  export GNUPGHOME="$(mktemp -d)"
fi
mkdir -p "${GNUPGHOME}"
chmod 700 "${GNUPGHOME}"

# Start a gpg-agent with loopback pinentry so we can feed the passphrase
# programmatically during signing. Must be configured before the first
# gpg command or the daemon caches the wrong setting.
cat > "${GNUPGHOME}/gpg-agent.conf" <<EOF
allow-loopback-pinentry
default-cache-ttl 7200
max-cache-ttl 86400
EOF
cat > "${GNUPGHOME}/gpg.conf" <<EOF
use-agent
pinentry-mode loopback
EOF

# Import the private key.
echo "${PACKAGES_GPG_PRIVATE_KEY}" | gpg --batch --import 2>&1

# Preset the passphrase so subsequent --detach-sign / --clearsign calls
# don't prompt. The keygrip lookup handles both primary key and subkeys.
# --preset and --no-use-agent together would be wrong; we want the agent
# to hold the passphrase for us.
gpg-connect-agent reloadagent /bye >/dev/null 2>&1 || true

# Trust the imported key to ultimate so apt-ftparchive doesn't warn.
echo -e "5\ny\n" | gpg --batch --command-fd 0 --no-tty --edit-key "${GPG_KEY_ID}" trust quit 2>/dev/null || true

# Confirm the key is present and matches the expected fingerprint.
actual_fpr=$(gpg --list-secret-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
if [[ "${actual_fpr}" != "${GPG_KEY_ID}" ]]; then
  echo "error: imported fingerprint '${actual_fpr}' does not match expected '${GPG_KEY_ID}'" >&2
  exit 1
fi

# Export GNUPGHOME to subsequent workflow steps.
if [[ -n "${GITHUB_ENV:-}" ]]; then
  echo "GNUPGHOME=${GNUPGHOME}" >> "${GITHUB_ENV}"
fi

echo "Imported signing key ${GPG_KEY_ID} into ${GNUPGHOME}"
