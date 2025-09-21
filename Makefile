# MCPProxy Makefile

.PHONY: help build frontend-build frontend-dev backend-dev clean test lint

# Default target
help:
	@echo "MCPProxy Build Commands:"
	@echo "  make build           - Build complete project (frontend + backend)"
	@echo "  make frontend-build  - Build frontend for production"
	@echo "  make frontend-dev    - Start frontend development server"
	@echo "  make backend-dev     - Build backend with dev flag (loads frontend from disk)"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make test           - Run tests"
	@echo "  make lint           - Run linter"

# Build complete project
build: frontend-build
	@echo "🔨 Building Go binary with embedded frontend..."
	go build -o mcpproxy ./cmd/mcpproxy
	go build -o mcpproxy-tray ./cmd/mcpproxy-tray
	@echo "✅ Build completed! Run: ./mcpproxy serve"
	@echo "🌐 Web UI: http://localhost:8080/ui/"

# Build frontend for production
frontend-build:
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
	@echo "📦 Installing frontend dependencies..."
	cd frontend && npm install
	@echo "✅ Development setup completed"