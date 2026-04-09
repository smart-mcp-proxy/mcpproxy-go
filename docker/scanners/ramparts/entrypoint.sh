#!/bin/sh
# Entrypoint for the Ramparts scanner container.
#
# MCPProxy mounts:
#   /scan/source — read-only, contains the server source tree.
#   /scan/report — writable, scanner writes SARIF here.
set -eu
exec ramparts scan \
  --format sarif \
  --output /scan/report/results.sarif \
  /scan/source
