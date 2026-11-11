#!/bin/sh
# Create the two file secrets the compose file mounts. Safe to re-run: existing
# files are kept, so a running stack's keys do not change under it.
set -eu
cd "$(dirname "$0")"
umask 077
if [ -L secrets ]; then
  echo "secrets is a symlink; refusing to use it" >&2
  exit 1
fi
mkdir -p secrets
# mkdir -p keeps the mode of an existing directory, so set it explicitly.
chmod 0700 secrets
for name in mcpproxy_api_key sidecar_api_key; do
  # -s and chmod follow symlinks, so a link here would expose (or overwrite)
  # a file outside secrets/. Accept only a regular file or nothing.
  if [ -L "secrets/$name" ] || { [ -e "secrets/$name" ] && [ ! -f "secrets/$name" ]; }; then
    echo "secrets/$name is not a regular file; refusing to use it" >&2
    exit 1
  fi
  if [ ! -s "secrets/$name" ]; then
    # 256 random bits as hex. od's status is checked on its own (a pipeline
    # reports only its last command) and the result must be exactly 64 hex
    # characters, so a failed or short read never becomes a weak key.
    raw="$(od -An -N32 -tx1 /dev/urandom)" || { echo "could not read /dev/urandom" >&2; exit 1; }
    key="$(printf '%s' "$raw" | tr -d ' \n')"
    case "$key" in
      *[!0-9a-f]*) echo "unexpected output from od" >&2; exit 1 ;;
    esac
    [ "${#key}" -eq 64 ] || { echo "short read from /dev/urandom" >&2; exit 1; }
    printf '%s' "$key" > "secrets/$name"
  fi
  # The sidecar runs as uid 1000 and mcpproxy as root, and both must read the
  # file through the bind mount. The directory (0700) is what keeps other host
  # users out, so the files themselves are world-readable.
  chmod 0644 "secrets/$name"
done
echo "secrets ready in $(pwd)/secrets"
