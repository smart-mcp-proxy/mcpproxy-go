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
# Builder stage: some dependencies (e.g. yara-python) publish no wheel for the
# current Python, so pip builds them from source and needs a C toolchain that
# the -slim image lacks. Build into a venv here, ship only the venv below.
FROM python:3.14-slim AS builder
RUN apt-get update \
    && apt-get install -y --no-install-recommends gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*
RUN python -m venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"

# Install the vendor package. We pin BOTH the top-level tool name and an exact
# version so the image is reproducible and cannot silently pull a new upstream
# release (supply-chain / rug-pull guard). Bump this pin deliberately.
RUN pip install --no-cache-dir cisco-ai-mcp-scanner==4.7.3

FROM python:3.14-slim

LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Cisco MCP Scanner (YARA + readiness) packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=builder /opt/venv /opt/venv
ENV PATH="/opt/venv/bin:$PATH"
# The CLI does not accept --version, so run `-h` as a sanity check that the
# binary is on PATH and imports cleanly.
RUN mcp-scanner -h >/dev/null

WORKDIR /scan
ENTRYPOINT ["mcp-scanner"]
