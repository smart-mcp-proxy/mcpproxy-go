# Snyk Agent Scan — tool poisoning, prompt injection, rug pull detector.
#
# Upstream: https://github.com/snyk/agent-scan (formerly Invariant Labs mcp-scan)
# Package:  https://pypi.org/project/snyk-agent-scan/
#
# Scans the tool definitions exported by MCPProxy at /scan/source/tools.json
# against the Snyk cloud API. Requires SNYK_TOKEN (user-provided, passed
# at runtime as an env var).
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-snyk:latest
FROM python:3.12-slim

LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Snyk Agent Scan packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Install the renamed vendor package. The CLI does not support --version,
# so we run `-h` as a build-time sanity check that the binary is on PATH.
RUN pip install --no-cache-dir snyk-agent-scan && snyk-agent-scan -h >/dev/null

WORKDIR /scan

# Default command: scan the exported tool definitions and emit JSON to
# stdout. The engine parses stdout via parseSnykAgentScanOutput().
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
