# Cisco MCP Scanner — YARA + readiness analyzers for MCP servers.
#
# Upstream: https://github.com/cisco-ai-defense/mcp-scanner
# Package:  https://pypi.org/project/cisco-ai-mcp-scanner/
#
# This is a thin wrapper that installs the published PyPI package and uses
# `mcp-scanner` as the container entrypoint. MCPProxy passes the --analyzers
# arg list at runtime (see registry_bundled.go).
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-cisco:latest
FROM python:3.12-slim

LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Cisco MCP Scanner (YARA + readiness) packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Install the vendor package. We pin BOTH the top-level tool name and an exact
# version so the image is reproducible and cannot silently pull a new upstream
# release (supply-chain / rug-pull guard). Bump this pin deliberately. The CLI
# does not accept --version, so we run `-h` as a sanity check that the binary is
# on PATH.
RUN pip install --no-cache-dir cisco-ai-mcp-scanner==4.7.3 && mcp-scanner -h >/dev/null

WORKDIR /scan
ENTRYPOINT ["mcp-scanner"]
