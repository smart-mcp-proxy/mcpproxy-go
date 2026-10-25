#!/usr/bin/env bash
# Pre-publish checks for the official MCP registry entry (Spec 121 FR-005).
#
#   scripts/registry-preflight.sh v0.71.1
#
# The registry stores each version once and never lets it be replaced, so a
# bad publish can't be fixed by publishing again. Before `mcp-publisher publish`
# this verifies that the image the stamped server.json points at
#   1. exists and is anonymously pullable,
#   2. carries the registry ownership label equal to the server.json name, and
#   3. starts with the exact env/port mapping server.json declares and answers
#      an MCP `initialize` handshake.
# Exit 0 = safe to publish; non-zero = print the cause and skip the publish.
#
# Env: IMAGE (default: first oci package of server.json, stamped for the tag)
#      SERVER_JSON (default server.json)  SMOKE_PORT (default 18391)
#      SKIP_SMOKE=1 / SKIP_LABEL=1 to run only one half (local debugging).
set -euo pipefail

TAG="${1:-}"
SERVER_JSON="${SERVER_JSON:-server.json}"
SMOKE_PORT="${SMOKE_PORT:-18391}"
HERE="$(cd "$(dirname "$0")" && pwd)"
LABEL_KEY="io.modelcontextprotocol.server.name"

die() { echo "registry-preflight: $*" >&2; exit 1; }

[[ -n "$TAG" ]] || die "usage: registry-preflight.sh vX.Y.Z"
STAMPED="$("$HERE/stamp-server-json.sh" "$TAG" "$SERVER_JSON")" || die "cannot stamp $SERVER_JSON for $TAG"
NAME="$(jq -r .name <<<"$STAMPED")"
IMAGE="${IMAGE:-$(jq -r '[.packages[] | select(.registryType=="oci")][0].identifier // empty' <<<"$STAMPED")}"
[[ -n "$IMAGE" ]] || die "server.json has no oci package"

# ---- 1+2: anonymous manifest + config label -------------------------------
if [[ "${SKIP_LABEL:-0}" != "1" ]]; then
  [[ "$IMAGE" == ghcr.io/* ]] || die "label check implemented for ghcr.io images only (got $IMAGE)"
  REF="${IMAGE#ghcr.io/}"; REPO="${REF%:*}"; TAGREF="${REF##*:}"
  TOKEN="$(curl -fsS "https://ghcr.io/token?service=ghcr.io&scope=repository:${REPO}:pull" | jq -r .token)" \
    || die "cannot get an anonymous pull token for ${REPO} (is the package private?)"
  [[ -n "$TOKEN" && "$TOKEN" != "null" ]] || die "empty anonymous token for ${REPO}"
  ACCEPT='application/vnd.oci.image.index.v1+json,application/vnd.docker.distribution.manifest.list.v2+json,application/vnd.oci.image.manifest.v1+json,application/vnd.docker.distribution.manifest.v2+json'
  fetch() { curl -fsSL -H "Authorization: Bearer $TOKEN" -H "Accept: $ACCEPT" "$1"; }
  MANIFEST="$(fetch "https://ghcr.io/v2/${REPO}/manifests/${TAGREF}")" \
    || die "image tag ${TAGREF} not found or not anonymously pullable: ${IMAGE}"
  if jq -e '.manifests' >/dev/null 2>&1 <<<"$MANIFEST"; then
    DIGEST="$(jq -r '[.manifests[] | select(.platform.os=="linux" and .platform.architecture=="amd64")][0].digest' <<<"$MANIFEST")"
    [[ -n "$DIGEST" && "$DIGEST" != "null" ]] || die "no linux/amd64 manifest in ${IMAGE}"
    MANIFEST="$(fetch "https://ghcr.io/v2/${REPO}/manifests/${DIGEST}")" || die "cannot fetch manifest ${DIGEST}"
  fi
  CFG="$(jq -r .config.digest <<<"$MANIFEST")"
  GOT="$(fetch "https://ghcr.io/v2/${REPO}/blobs/${CFG}" | jq -r --arg k "$LABEL_KEY" '.config.Labels[$k] // empty')" \
    || die "cannot read image config ${CFG}"
  [[ "$GOT" == "$NAME" ]] || die "image ${IMAGE} label ${LABEL_KEY}='${GOT}', want '${NAME}' (registry would reject the publish)"
  echo "ok: ${IMAGE} is anonymously pullable and carries ${LABEL_KEY}=${NAME}"
fi

# ---- 3: smoke test with the declared env / port ----------------------------
if [[ "${SKIP_SMOKE:-0}" != "1" ]]; then
  command -v docker >/dev/null || die "docker is required for the smoke test (SKIP_SMOKE=1 to bypass)"
  ENVS=()
  while IFS= read -r line; do ENVS+=("-e" "$line"); done < <(jq -r '[.packages[] | select(.registryType=="oci")][0].environmentVariables // [] | .[] | "\(.name)=\(.value // "")"' <<<"$STAMPED")
  CID="$(docker run -d --rm -p "127.0.0.1:${SMOKE_PORT}:8080" ${ENVS[@]+"${ENVS[@]}"} "$IMAGE")" || die "docker run failed for ${IMAGE}"
  trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT
  URL="$(jq -r '[.packages[] | select(.registryType=="oci")][0].transport.url' <<<"$STAMPED" | sed "s#localhost:8080#127.0.0.1:${SMOKE_PORT}#")"
  BODY='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"registry-preflight","version":"1"}}}'
  OK=""
  for _ in $(seq 1 30); do
    RESP="$(curl -sS -m 5 -X POST "$URL" -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d "$BODY" 2>/dev/null || true)"
    if grep -q '"serverInfo"' <<<"$RESP"; then OK=1; break; fi
    sleep 1
  done
  [[ -n "$OK" ]] || { docker logs "$CID" 2>&1 | tail -20 >&2 || true; die "MCP initialize against ${URL} did not succeed within 30s"; }
  echo "ok: ${IMAGE} answered MCP initialize at ${URL}"
fi
