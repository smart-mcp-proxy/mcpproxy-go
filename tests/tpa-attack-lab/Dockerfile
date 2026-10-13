# Container image for the TPA attack lab fixture (tests/tpa-attack-lab).
# Build from the REPO ROOT so the Go module context is complete:
#
#   docker build -f tests/tpa-attack-lab/Dockerfile -t tpa-attack-lab:lab .
#
# Safety model:
#   - runtime is distroless/static: no shell, no coreutils, no package manager
#     — even a fully weaponized fixture binary could not `rm` files or `curl`
#     out, because the tools are simply not present in the image
#   - the fixture speaks MCP over stdio only, so its container can (and should)
#     run with --network none — see tests/tpa-lab-config-docker.json
#   - no ENTRYPOINT on purpose (mcpproxy's isolation spawns
#     `docker run … <image> /tpa-attack-lab --role …`, and an ENTRYPOINT would
#     prefix the argv); CMD is only the bare-docker default, mirroring the
#     cmd/mcpfixture image conventions

FROM golang:1.27-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /tpa-attack-lab ./tests/tpa-attack-lab

FROM gcr.io/distroless/static-debian12

COPY --from=builder /tpa-attack-lab /tpa-attack-lab

CMD ["/tpa-attack-lab", "--role", "attacker", "--stage", "1"]
