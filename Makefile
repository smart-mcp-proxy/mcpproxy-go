# MCPProxy Makefile

.PHONY: help build build-server build-docker build-deb swagger swagger-verify frontend-build frontend-dev backend-dev clean test test-coverage test-e2e test-e2e-oauth lint dev-setup docs-setup docs-dev docs-build docs-clean

SWAGGER_BIN ?= $(HOME)/go/bin/swag
SWAGGER_OUT ?= oas
SWAGGER_ENTRY ?= cmd/mcpproxy/main.go

# Default target
help:
	@echo "MCPProxy Build Commands:"
	@echo "  make build           - Build complete project (swagger + frontend + backend)"
	@echo "  make swagger         - Generate OpenAPI specification"
	@echo "  make swagger-verify  - Regenerate OpenAPI and fail if artifacts are dirty"
	@echo "  make frontend-build  - Build frontend for production"
	@echo "  make frontend-dev    - Start frontend development server"
	@echo "  make backend-dev     - Build backend with dev flag (loads frontend from disk)"
	@echo "  make clean           - Clean build artifacts"
	@echo "  make test            - Run unit tests"
	@echo "  make test-coverage   - Run tests with coverage"
	@echo "  make test-e2e        - Run all E2E tests"
	@echo "  make test-e2e-oauth  - Run OAuth E2E tests with Playwright"
	@echo "  make lint            - Run linter"
	@echo "  make dev-setup       - Install development dependencies (swag, frontend, Playwright)"
	@echo ""
	@echo "Server Edition:"
	@echo "  make build-server    - Build Server edition binary (with -tags server)"
	@echo "  make build-docker    - Build Server Docker image"
	@echo "  make build-deb       - Build Server .deb package (TODO)"
	@echo ""
	@echo "Documentation Commands:"
	@echo "  make docs-setup      - Install documentation dependencies"
	@echo "  make docs-dev        - Start docs dev server (http://localhost:3000)"
	@echo "  make docs-build      - Build documentation site locally"
	@echo "  make docs-clean      - Clean documentation build artifacts"

# Generate OpenAPI specification
swagger:
	@echo "📚 Generating OpenAPI 3.1 specification..."
	@[ -x "$(SWAGGER_BIN)" ] || { echo "⚠️  swag binary not found at $(SWAGGER_BIN). Run 'go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4'"; exit 1; }
	@mkdir -p $(SWAGGER_OUT)
	$(SWAGGER_BIN) init -g $(SWAGGER_ENTRY) --output $(SWAGGER_OUT) --outputTypes go,yaml --v3.1 --exclude specs
	@echo "✅ OpenAPI 3.1 spec generated: $(SWAGGER_OUT)/swagger.yaml and $(SWAGGER_OUT)/docs.go"

swagger-verify: swagger
	@echo "🔎 Verifying OpenAPI artifacts are committed..."
	@if git status --porcelain -- $(SWAGGER_OUT)/swagger.yaml $(SWAGGER_OUT)/docs.go | grep -q .; then \
		echo "❌ OpenAPI artifacts are out of date. Run 'make swagger' and commit the regenerated files."; \
		git diff --stat -- $(SWAGGER_OUT)/swagger.yaml $(SWAGGER_OUT)/docs.go || true; \
		exit 1; \
	fi
	@echo "✅ OpenAPI artifacts are up to date."

# Version detection for builds
VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.1.0-dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE) -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=$(VERSION) -s -w

# Build complete project
build: swagger frontend-build
	@echo "🔨 Building Go binary with embedded frontend (version: $(VERSION))..."
	go build -ldflags "$(LDFLAGS)" -o mcpproxy ./cmd/mcpproxy
	go build -ldflags "$(LDFLAGS)" -o mcpproxy-tray ./cmd/mcpproxy-tray
	@echo "✅ Build completed! Run: ./mcpproxy serve"
	@echo "🌐 Web UI: http://localhost:8080/ui/"
	@echo "📚 API Docs: http://localhost:8080/swagger/"

# Build frontend for production
frontend-build:
	@echo "🎨 Generating TypeScript types from Go contracts..."
	go run ./cmd/generate-types
	@echo "🎨 Building frontend for production..."
	cd frontend && npm install && npm run build
	@echo "📁 Copying dist files for embedding..."
	rm -rf web/frontend
	mkdir -p web/frontend
	cp -r frontend/dist web/frontend/
	@echo "✅ Frontend build completed"

# Start frontend development server
frontend-dev:
	@echo "🎨 Starting frontend development server..."
	cd frontend && npm install && npm run dev

# Build backend with dev flag (for development with frontend hot reload)
backend-dev:
	@echo "🔨 Building backend in development mode (version: $(VERSION))..."
	go build -tags dev -ldflags "$(LDFLAGS)" -o mcpproxy-dev ./cmd/mcpproxy
	@echo "✅ Development backend ready!"
	@echo "🚀 Run: ./mcpproxy-dev serve"
	@echo "🌐 In dev mode, make sure frontend dev server is running on port 3000"

# Build Server edition
build-server: swagger frontend-build
	@echo "🔨 Building Server edition binary (version: $(VERSION))..."
	go build -tags server -ldflags "$(LDFLAGS)" -o mcpproxy-server ./cmd/mcpproxy
	@echo "✅ Server build completed! Run: ./mcpproxy-server serve"

# Build Server Docker image
build-docker:
	@echo "🐳 Building Server Docker image (version: $(VERSION))..."
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t mcpproxy-server:$(VERSION) -t mcpproxy-server:latest .
	@echo "✅ Docker image built: mcpproxy-server:$(VERSION)"

# Build Server .deb package (placeholder)
build-deb:
	@echo "📦 Building Server .deb package..."
	@echo "⚠️  TODO: Implement deb package build (nfpm or dpkg-deb)"
	@echo "   See: https://nfpm.goreleaser.com/"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -f mcpproxy mcpproxy-dev mcpproxy-tray mcpproxy-server
	rm -rf frontend/dist frontend/node_modules web/frontend
	go clean
	@echo "✅ Cleanup completed"

# Run tests
test:
	@echo "🧪 Running Go tests..."
	go test ./internal/... -v
	@echo "🧪 Running frontend tests..."
	cd frontend && npm install && npm run test

# Run tests with coverage
test-coverage:
	@echo "🧪 Running tests with coverage..."
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -html=coverage.out -o coverage.html
	cd frontend && npm install && npm run coverage

# Run linter
lint:
	@echo "🔍 Running Go linter..."
	golangci-lint run ./...
	@echo "🔍 Running frontend linter..."
	cd frontend && npm install && npm run lint

# Install development dependencies
dev-setup:
	@echo "🛠️  Setting up development environment..."
	@echo "📦 Installing swag (OpenAPI generator)..."
	go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4
	@echo "📦 Installing frontend dependencies..."
	cd frontend && npm install
	@echo "📦 Installing Playwright E2E test dependencies..."
	cd e2e/playwright && npm install
	@echo "📦 Installing Playwright browsers..."
	cd e2e/playwright && npx playwright install chromium
	@echo "📦 Installing pre-commit hooks..."
	@if command -v prek >/dev/null 2>&1; then \
		prek install && prek install --hook-type pre-push; \
		echo "✅ Git hooks installed (prek)"; \
	else \
		echo "⚠️  prek not found. Install with: brew install prek"; \
	fi
	@echo "✅ Development setup completed"

# Run OAuth E2E tests with Playwright
test-e2e-oauth:
	@echo "🧪 Running OAuth E2E tests..."
	./scripts/run-oauth-e2e.sh

# Run all E2E tests
test-e2e: test-e2e-oauth
	@echo "🧪 Running E2E tests..."
	./scripts/test-api-e2e.sh

# Documentation site commands
docs-setup:
	@echo "📦 Installing documentation dependencies..."
	@if [ ! -d "website" ]; then echo "❌ website/ directory not found. Run after Phase 1 setup."; exit 1; fi
	cd website && npm install
	@echo "✅ Documentation setup complete"

docs-dev:
	@echo "📄 Starting documentation dev server..."
	@if [ ! -d "website" ]; then echo "❌ website/ directory not found. Run after Phase 1 setup."; exit 1; fi
	cd website && ./prepare-docs.sh && npm run start

docs-build:
	@echo "🔨 Building documentation site..."
	@if [ ! -d "website" ]; then echo "❌ website/ directory not found. Run after Phase 1 setup."; exit 1; fi
	cd website && ./prepare-docs.sh && npm run build
	@echo "✅ Documentation built to website/build/"

docs-clean:
	@echo "🧹 Cleaning documentation artifacts..."
	rm -rf website/build website/.docusaurus website/node_modules website/docs
	@echo "✅ Documentation cleanup complete"
