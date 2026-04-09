# Ramparts MCP Scanner — Rust-based YARA scanner for MCP servers.
#
# Upstream: https://github.com/getjavelin/ramparts
# Crate:    https://crates.io/crates/ramparts
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-ramparts:latest
#
# We use a two-stage build: a Rust builder installs the `ramparts` crate
# and the final image ships a slim Debian with just the binary.
FROM rust:1-slim AS builder
RUN apt-get update && apt-get install -y --no-install-recommends \
        pkg-config libssl-dev ca-certificates git && \
    rm -rf /var/lib/apt/lists/*
RUN cargo install ramparts --locked --root /opt/ramparts

FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Ramparts MCP Scanner packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Proprietary"

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates libssl3 && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /opt/ramparts/bin/ramparts /usr/local/bin/ramparts
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /scan
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
