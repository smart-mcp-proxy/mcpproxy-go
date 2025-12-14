# MCPProxy Makefile

.PHONY: help build swagger swagger-verify frontend-build frontend-dev backend-dev clean test test-coverage test-e2e test-e2e-oauth lint dev-setup docs-setup docs-dev docs-build docs-clean

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

# Build complete project
build: swagger frontend-build
	@echo "🔨 Building Go binary with embedded frontend..."
	go build -o mcpproxy ./cmd/mcpproxy
	go build -o mcpproxy-tray ./cmd/mcpproxy-tray
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
	@echo "🔨 Building backend in development mode..."
	go build -tags dev -o mcpproxy-dev ./cmd/mcpproxy
	@echo "✅ Development backend ready!"
	@echo "🚀 Run: ./mcpproxy-dev serve"
	@echo "🌐 In dev mode, make sure frontend dev server is running on port 3000"

# Clean build artifacts
clean:
	@echo "🧹 Cleaning build artifacts..."
	rm -f mcpproxy mcpproxy-dev mcpproxy-tray
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
