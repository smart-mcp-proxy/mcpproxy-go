#!/bin/sh
# Entrypoint for the Snyk Agent Scan scanner container.
#
# MCPProxy mounts:
#   /scan/source — read-only, contains `tools.json` with exported tool defs.
#   /scan/report — writable, the scanner writes results.json here.
#
# Environment:
#   SNYK_TOKEN — required, user-supplied API token.
set -eu

if [ -z "${SNYK_TOKEN:-}" ]; then
  echo "error: SNYK_TOKEN is not set. Configure it in the MCPProxy Security page." >&2
  exit 2
fi

TOOLS_JSON="/scan/source/tools.json"
if [ ! -f "$TOOLS_JSON" ]; then
  echo "error: $TOOLS_JSON not found — nothing to scan" >&2
  exit 3
fi

# `snyk-agent-scan scan` emits JSON on stdout. MCPProxy captures stdout and
# parses it with parseSnykAgentScanOutput in engine.go.
exec snyk-agent-scan scan --json "$TOOLS_JSON"
