#!/usr/bin/env bash
# Smoke-test the listen address of the server Docker image (#1942).
#
# Usage: scripts/test-docker-listen.sh [image]   (default: mcpproxy-server:pr)
#
# Checks, against a real container each time:
#   1. default            -> listens on 0.0.0.0:8080
#   2. -e MCPPROXY_LISTEN -> listens on the env address, not on 8080
#   3. --listen + env     -> the CLI flag wins over MCPPROXY_LISTEN
# The image is distroless (no shell, no curl), so every probe goes through a
# published port from the host.
set -euo pipefail

IMAGE="${1:-mcpproxy-server:pr}"
PREFIX="mcpproxy-listen-smoke-$$"
containers=()

cleanup() {
  for c in "${containers[@]:-}"; do
    [ -n "$c" ] && docker rm -f "$c" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

# start <name> <docker run args...> -- <container args...>
start() {
  local name="$PREFIX-$1"; shift
  local run_args=() cmd_args=()
  while [ $# -gt 0 ] && [ "$1" != "--" ]; do run_args+=("$1"); shift; done
  [ $# -gt 0 ] && shift
  cmd_args=("$@")
  containers+=("$name")
  docker run -d --name "$name" \
    -e MCPPROXY_API_KEY=listen-smoke-key \
    -e MCPPROXY_TELEMETRY=false \
    "${run_args[@]}" "$IMAGE" ${cmd_args[@]+"${cmd_args[@]}"} >/dev/null
}

# expect_up <container> <host port>: /healthz answers within 60s.
expect_up() {
  local name="$PREFIX-$1" port="$2"
  for _ in $(seq 1 60); do
    if curl -fsS --max-time 2 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
      echo "ok: $1 answers on host port $port"
      return 0
    fi
    if [ "$(docker inspect -f '{{.State.Running}}' "$name")" != "true" ]; then
      echo "FAIL: $1 exited" >&2; docker logs "$name" >&2 || true; return 1
    fi
    sleep 1
  done
  echo "FAIL: $1 did not answer on host port $port" >&2
  docker logs "$name" >&2 || true
  return 1
}

# expect_down <case> <host port>: nothing in the container listens behind it.
expect_down() {
  if curl -fsS --max-time 2 "http://127.0.0.1:$2/healthz" >/dev/null 2>&1; then
    echo "FAIL: $1 unexpectedly answers on host port $2" >&2
    return 1
  fi
  echo "ok: $1 does not answer on host port $2"
}

# 1. Default: the image listens on 0.0.0.0:8080.
start default -p 127.0.0.1:18080:8080
expect_up default 18080

# 2. MCPPROXY_LISTEN moves the listener; 8080 is no longer bound.
start env -e MCPPROXY_LISTEN=0.0.0.0:9090 \
  -p 127.0.0.1:18090:9090 -p 127.0.0.1:18091:8080
expect_up env 18090
expect_down env 18091

# 3. --listen (container args) outranks MCPPROXY_LISTEN.
start flag -e MCPPROXY_LISTEN=0.0.0.0:9090 \
  -p 127.0.0.1:18070:7070 -p 127.0.0.1:18071:9090 -- --listen 0.0.0.0:7070
expect_up flag 18070
expect_down flag 18071

echo "docker listen smoke test passed for $IMAGE"
