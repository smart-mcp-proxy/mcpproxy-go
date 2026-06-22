#!/bin/sh
# Entrypoint for the Ramparts scanner container (v0.8.x — URL/stdio model).
#
# Ramparts v0.8.x dropped file/directory scanning. `ramparts scan <target>` now
# expects a LIVE MCP endpoint (an HTTP URL or a `stdio:` subprocess): it runs the
# MCP handshake, enumerates the advertised tools/resources/prompts, and analyzes
# them with YARA rules (+ optional LLM). The old `--format sarif --output FILE
# /scan/source` invocation is invalid in v0.8.x (no `sarif` format, no `--output`
# flag, and a directory is not a valid scan target).
#
# MCPProxy must NOT re-execute the untrusted upstream just to give Ramparts a
# target (that would violate the "never execute scanned source" invariant,
# MCP-2206/#658). Instead the engine exports the tool definitions it already
# captured from the running upstream into /scan/source/tools.json (the same file
# the Cisco scanner consumes), and we replay them to Ramparts over stdio via
# mcp-replay.py — a static shim that runs no upstream code.
#
# MCPProxy mounts:
#   /scan/source — read-only; contains tools.json (captured tool definitions).
#   /scan/report — writable; we write the scanner's native JSON report here.
#
# Ramparts loads its YARA rules (./rules), taxonomies (./taxonomies) and default
# config (./config.yaml) relative to the working directory — the image ships
# them at /scan and WORKDIR is /scan (see Dockerfile).
set -u

# Overridable so the test harness (entrypoint_test.sh) can run without Docker.
REPORT_DIR="${REPORT_DIR:-/scan/report}"
REPLAY="${MCP_REPLAY:-/usr/local/bin/mcp-replay.py}"
REPORT="$REPORT_DIR/results.json"
RAMPARTS_STDERR="$REPORT_DIR/ramparts.stderr"

# When MCP_SERVER_URL is set, scan the live MCP endpoint directly (HTTP/SSE
# servers). Otherwise replay the captured tool definitions over stdio (stdio
# servers). The URL target lets Ramparts enumerate tools, resources, AND prompts
# from the live server, giving richer analysis than the replay shim alone.
TARGET="${MCP_SERVER_URL:-stdio:python3:$REPLAY}"

# fail_closed marks the scan as FAILED. The Go scanner runner ignores the
# container exit code (docker.go RunScanner returns nil for a non-zero exit),
# so a non-zero exit alone is NOT enough — it reads whatever report file exists.
# We therefore DELETE the report so the engine finds no output and records the
# scanner as failed instead of parsing a stale/error payload as a clean scan
# (MCP-2443: defeat the fail-open).
fail_closed() {
  echo "ramparts scan FAILED (fail-closed): $1" >&2
  echo "ramparts exit code: ${rc:-unknown}" >&2
  if [ -f "$RAMPARTS_STDERR" ]; then
    echo "--- ramparts stderr ---" >&2
    cat "$RAMPARTS_STDERR" >&2 2>/dev/null || true
  fi
  rm -f "$REPORT"
  exit 1
}

# `scan`'s --format accepts json|raw|table|text (no sarif) and has no --output
# flag, so emit native JSON to stdout and capture it. MCPProxy's engine parses
# the Ramparts JSON shape ({security_issues{...}, yara_results[]}) directly.
# Ramparts may exit non-zero to signal findings/offline-LLM, so capture the code
# rather than abort; the gate below is the report's shape, not the bare exit.
#
# When MCP_SERVER_URL is set, the target is a live HTTP/SSE endpoint and the
# container needs network access (NetworkReq: true in the scanner registry).
# Otherwise the target is the stdio replay shim (offline, no network needed).
ramparts scan "$TARGET" \
  --format json \
  > "$REPORT" 2>"$RAMPARTS_STDERR"
rc=$?

if [ ! -s "$REPORT" ]; then
  fail_closed "ramparts produced no report"
fi

# Validate $REPORT is a GENUINE Ramparts report, not an error payload. A real
# report carries a top-level "security_issues" OBJECT (with the tool/prompt/
# resource issue arrays) and a "yara_results" ARRAY. We TYPE-check, not just
# presence-check: an error payload such as {"security_issues":"scan failed"}
# has the key present but the wrong type and must fail CLOSED — a presence-only
# gate would wave it through as a spurious clean scan (MCP-2443 re-review).
# python3 is already in the image for the replay shim.
if ! python3 - "$REPORT" <<'PY'
import json, sys
try:
    with open(sys.argv[1], "r", encoding="utf-8") as fh:
        data = json.load(fh)
except (OSError, ValueError) as exc:
    print(f"report is not valid JSON: {exc}", file=sys.stderr)
    sys.exit(1)
if (not isinstance(data, dict)
        or not isinstance(data.get("security_issues"), dict)
        or not isinstance(data.get("yara_results"), list)):
    print("report is not a Ramparts report: security_issues must be an object "
          "and yara_results an array (error payload or wrong shape)", file=sys.stderr)
    sys.exit(1)
sys.exit(0)
PY
then
  fail_closed "report is not a valid Ramparts report (error payload or wrong shape)"
fi

# A valid report with rc != 0 means Ramparts signalled findings/offline-LLM — a
# real result we keep. (rc != 0 with no valid report already failed above.)
if [ "$rc" -ne 0 ]; then
  echo "ramparts exited $rc but produced a valid report (findings/offline-LLM); keeping it" >&2
fi
