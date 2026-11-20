# One stdio MCP server wrapped to streamable HTTP by supergateway.
# Change SERVER_PKG (and the --stdio command in docker-compose.yml) to wrap a
# different server; run one sidecar per server.
FROM node:24-bookworm-slim@sha256:01d566e8d94d8c619591d78652babfccd3c6992a3dffa9ed5ab39e11cf6c70e9

ARG SUPERGATEWAY_VERSION=4.2.0
ARG SERVER_PKG=@modelcontextprotocol/server-everything@2.3.0

RUN npm install -g --no-audit --no-fund "supergateway@${SUPERGATEWAY_VERSION}" "${SERVER_PKG}" \
 && npm cache clean --force

# The base image's unprivileged user (uid 1000). The compose file also runs the
# container read-only with every capability dropped.
USER node
WORKDIR /home/node
ENTRYPOINT ["supergateway"]
