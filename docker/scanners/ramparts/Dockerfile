# Ramparts MCP Scanner — Rust-based YARA scanner for MCP servers (v0.8.x).
#
# Upstream: https://github.com/getjavelin/ramparts
# Crate:    https://crates.io/crates/ramparts
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-ramparts:latest
#
# Two-stage build: a Rust builder installs the `ramparts` crate and clones the
# matching source tag; the final slim image ships the binary, its runtime
# assets, and a static MCP stdio replay shim.
#
# Why we ship more than the binary (v0.8.x):
#   - Ramparts loads its YARA rules, taxonomies, and default config from the
#     working directory at runtime (./rules, ./taxonomies, ./config.yaml).
#     `cargo install` only produces the binary, so without these the scanner
#     loads ZERO rules and reports no findings. We copy them from the source
#     tag that matches the installed crate version.
#   - v0.8.x scans a live MCP endpoint, not a directory. MCPProxy never
#     re-executes the untrusted upstream; instead the entrypoint points Ramparts
#     at `stdio:python3:/usr/local/bin/mcp-replay.py`, a static shim that replays
#     the already-captured /scan/source/tools.json over the MCP protocol. That
#     needs a python3 runtime in the final image.
#
# IMPORTANT: the builder's Debian release MUST match the runtime stage
# (`debian:bookworm-slim`, glibc 2.36). The unpinned `rust:1-slim` tag now
# resolves to Debian trixie (glibc 2.39+), so the binary it produces fails at
# runtime on bookworm with `GLIBC_2.39 not found` (MCP-2395). Pinning the
# builder to `-bookworm` keeps both stages on the same glibc.
ARG RAMPARTS_VERSION=0.8.2

FROM rust:1-slim-bookworm AS builder
ARG RAMPARTS_VERSION
RUN apt-get update && apt-get install -y --no-install-recommends \
        pkg-config libssl-dev ca-certificates git && \
    rm -rf /var/lib/apt/lists/*
RUN cargo install ramparts --version "${RAMPARTS_VERSION}" --locked --root /opt/ramparts
# Clone the SAME version tag to obtain the runtime assets (rules/taxonomies/
# config) that the installed binary expects to find relative to its CWD.
RUN git clone --depth 1 --branch "${RAMPARTS_VERSION}" \
        https://github.com/getjavelin/ramparts /opt/ramparts-src

FROM debian:bookworm-slim
LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Ramparts MCP Scanner packaged for MCPProxy"
LABEL org.opencontainers.image.licenses="Proprietary"

# python3 powers the static MCP stdio replay shim (mcp-replay.py); no pip needed.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates libssl3 python3 && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /opt/ramparts/bin/ramparts /usr/local/bin/ramparts
# Runtime assets loaded relative to WORKDIR (/scan). The /scan/source and
# /scan/report bind mounts attach as subdirectories at runtime and do not
# shadow these baked-in files.
COPY --from=builder /opt/ramparts-src/rules /scan/rules
COPY --from=builder /opt/ramparts-src/taxonomies /scan/taxonomies
COPY --from=builder /opt/ramparts-src/config.yaml /scan/config.yaml

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
COPY mcp-replay.py /usr/local/bin/mcp-replay.py
RUN chmod +x /usr/local/bin/entrypoint.sh /usr/local/bin/mcp-replay.py

WORKDIR /scan
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
