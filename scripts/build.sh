#!/bin/bash
set -e

# Get version from git tag, or use default
VERSION=${1:-$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.1.0-dev")}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

echo "Building mcpproxy version: $VERSION"
echo "Commit: $COMMIT"
echo "Date: $DATE"

# Build with version injection
go build -ldflags "-X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE -s -w" -o mcpproxy ./cmd/mcpproxy

echo "Build complete: mcpproxy"
echo "Test version info:"
./mcpproxy --version 