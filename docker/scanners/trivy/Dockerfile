# Trivy MCP Scanner — wraps the upstream Aqua image with a pre-cached
# vulnerability database so scans don't race the download timeout on first run.
#
# Upstream: https://github.com/aquasecurity/trivy
# Base:     ghcr.io/aquasecurity/trivy:latest
#
# Published as: ghcr.io/smart-mcp-proxy/scanner-trivy:latest
# Rebuilt weekly via scanner-images.yml schedule to keep the DB current.
#
# Why a custom wrapper (MCP-2150):
#   The upstream image downloads its ~96 MiB vuln DB on first scan. On slow
#   connections this races the per-scanner timeout and causes exit 1 with no
#   output. Baking the DB into the image eliminates the download entirely for
#   filesystem scans (trivy fs) and avoids the race for image scans too.
#
# Runtime notes:
#   TRIVY_CACHE_DIR is set here so the trivy binary picks up the baked-in DB
#   at the same path without needing --cache-dir flags in the scan command.
#   NetworkReq is still required for `trivy image` scanning (pulling remote
#   container images from registries) but NOT for `trivy fs` (vuln DB is local).
FROM ghcr.io/aquasecurity/trivy:latest
LABEL org.opencontainers.image.source="https://github.com/smart-mcp-proxy/mcpproxy-go"
LABEL org.opencontainers.image.description="Trivy vulnerability scanner with pre-cached vuln DB for MCPProxy"
LABEL org.opencontainers.image.licenses="Apache-2.0"

# Pre-cache the vulnerability database at image build time.
# --download-db-only: fetch the DB and exit, no scan performed (trivy 0.34+).
# --no-progress: suppress the progress bar so build logs are clean.
ENV TRIVY_CACHE_DIR=/trivy-cache
RUN trivy --cache-dir /trivy-cache image --download-db-only --no-progress
