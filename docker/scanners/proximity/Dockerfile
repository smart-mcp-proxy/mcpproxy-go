# Nova Proximity — keyword-based offline MCP security scanner.
#
# Upstream: https://github.com/fr0gger/proximity
#
# The upstream is a small Python script that applies NOVA-inspired rules.
# We bundle it into a minimal Python image so MCPProxy users get a
# fully-offline scanner with no API key requirement.
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-proximity:latest
FROM python:3.12-slim

LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Nova Proximity (keyword MCP scanner) packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

RUN pip install --no-cache-dir novahunter proximity-mcp 2>/dev/null || \
    pip install --no-cache-dir requests pyyaml

WORKDIR /scan
COPY entrypoint.py /usr/local/bin/entrypoint.py
RUN chmod +x /usr/local/bin/entrypoint.py
ENTRYPOINT ["python", "/usr/local/bin/entrypoint.py"]
