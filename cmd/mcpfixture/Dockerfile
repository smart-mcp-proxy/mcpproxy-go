# Dockerfile for the release QA gate's docker-isolated stdio fixture cell
# (Spec 081, FR-006/FR-009). Built by scripts/gate/build-fixture-image.sh,
# which cross-compiles a fully static linux binary (CGO_ENABLED=0, pure Go)
# BEFORE the docker build, so this image needs no toolchain and no third-party
# pulls at all — FROM scratch.
#
# IMPORTANT — no ENTRYPOINT, on purpose. mcpproxy's Docker isolation
# (internal/upstream/core/isolation.go BuildDockerArgs +
# connection_docker.go setupDockerIsolation) always appends the container
# command explicitly after the image:
#
#   docker run --rm -i --name mcpproxy-<server>-<rand> <labels> ... \
#     docker.io/library/mcpfixture:gate /mcpfixture --transport stdio
#
# An ENTRYPOINT would prefix that argv and break it. CMD only provides the
# default for a bare `docker run mcpfixture:gate` smoke test.
FROM scratch
COPY mcpfixture /mcpfixture
CMD ["/mcpfixture", "--transport", "stdio"]
