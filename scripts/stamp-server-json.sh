#!/usr/bin/env bash
# Stamp server.json for a release tag (Spec 121 FR-004).
#
#   scripts/stamp-server-json.sh v0.71.1 [server.json] > stamped.json
#
# - .version                      <- tag without the leading "v" (registry rule)
# - every oci package identifier  <- tag WITH its "v" (the image tag spelling
#                                    release.yml build-docker pushes)
# Used by BOTH registry publishers (release.yml and retry-sign-release.yml) so
# a stale committed identifier can never reach the immutable registry.
set -euo pipefail

TAG="${1:-}"
IN="${2:-server.json}"

if [[ ! "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "stamp-server-json: tag must be a stable vX.Y.Z (got '${TAG}')" >&2
  exit 2
fi
if [[ ! -f "$IN" ]]; then
  echo "stamp-server-json: $IN not found" >&2
  exit 2
fi
if [[ "$(jq '(.packages // []) | length' "$IN")" -eq 0 ]]; then
  echo "stamp-server-json: $IN has no packages; refusing to publish an install-less registry entry" >&2
  exit 3
fi

jq --arg v "${TAG#v}" --arg t "$TAG" '
  .version = $v
  | .packages |= map(
      if .registryType == "oci"
      then .identifier |= sub(":[^:/]+$"; ":" + $t)
      else . end)
' "$IN"
