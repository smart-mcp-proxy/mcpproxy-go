# Build stage
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git nodejs npm make

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build frontend
RUN cd frontend && npm ci && npm run build
RUN mkdir -p web/frontend && cp -r frontend/dist web/frontend/

# Build server edition binary
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 go build \
    -tags server \
    -ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE} -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=${VERSION} -s -w" \
    -o /mcpproxy ./cmd/mcpproxy

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /mcpproxy /usr/local/bin/mcpproxy

EXPOSE 8080

ENTRYPOINT ["mcpproxy", "serve", "--listen", "0.0.0.0:8080"]
