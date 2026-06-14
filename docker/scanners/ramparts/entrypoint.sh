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

REPORT=/scan/report/results.json

# `scan`'s --format accepts json|raw|table|text (no sarif) and has no --output
# flag, so emit native JSON to stdout and capture it. MCPProxy's engine parses
# the Ramparts JSON shape ({security_issues{...}, yara_results[]}) directly.
# A non-zero exit from findings/offline-LLM is expected and tolerated by the
# engine as long as a report was produced, so don't let `set -e` abort here.
ramparts scan "stdio:python3:/usr/local/bin/mcp-replay.py" \
  --format json \
  > "$REPORT" 2>/scan/report/ramparts.stderr || true

if [ ! -s "$REPORT" ]; then
  echo "ramparts produced no report; stderr follows:" >&2
  cat /scan/report/ramparts.stderr >&2 2>/dev/null || true
  exit 1
fi
