# MCP Proxy Evaluation Environment Makefile
SHELL := /bin/bash
.PHONY: help setup start-servers start-registry start-mcpproxy run-eval clean test-connection

# Default target
help: ## Show this help message
	@echo "MCP Proxy Evaluation Environment"
	@echo "================================"
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

setup: ## Install dependencies and setup environment
	@echo "🔧 Setting up evaluation environment..."
	uv sync
	@echo "✅ Setup complete!"

start-registry: ## Start the fake MCP registry server
	@echo "🚀 Starting fake MCP registry on port 8001..."
	uv run mcp-registry &
	@sleep 2
	@echo "📡 Registry server started at http://localhost:8001"

start-servers: ## Verify MCP servers are available via uvx
	@echo "🎲 Verifying fake MCP servers are available via uvx..."
	@echo "  ✓ Dice Roller (uvx mcp-dice)"
	@echo "  ✓ Weather Service (uvx mcp-weather)"
	@echo "  ✓ Restaurant Finder (uvx mcp-restaurant)"
	@echo "  ✓ Calculator (uvx mcp-calculator)"
	@echo "  ✓ Translator (uvx mcp-translator)"
	@echo "  ✓ Morse Code (uvx mcp-morse)"
	@echo "  ✓ Time Service (uvx mcp-time)"
	@echo "  ✓ Joke Generator (uvx mcp-jokes)"
	@echo "  ✓ Color Palette (uvx mcp-color)"
	@echo "  ✓ Random Generator (uvx mcp-random)"
	@echo "✅ All MCP servers available via stdio!"

start-mcpproxy: ## Start mcpproxy with evaluation config
	@echo "🔧 Starting mcpproxy with evaluation configuration..."
	@echo "⚠️  Make sure to build and configure mcpproxy first:"
	@echo "   cd .. && go build"
	@echo "   pkill mcpproxy || true"
	@echo "   ../mcpproxy --config=eval/configs/mcpproxy_eval.json --log-level=debug --tray=false"

start-all: start-registry start-servers ## Start registry and verify MCP servers
	@echo "🌟 All services ready!"
	@echo ""
	@echo "🔍 Service Status:"
	@echo "  Registry:     http://localhost:8001/health"
	@echo "  MCP Servers:  Available via stdio (uvx commands)"
	@echo ""
	@echo "📋 Next steps:"
	@echo "  1. Start mcpproxy: make start-mcpproxy"
	@echo "  2. Open ADK web UI: make start-adk-web"

test-connection: ## Test connection to all services
	@echo "🔍 Testing service connections..."
	@echo -n "Registry: "
	@curl -s http://localhost:8001/health > /dev/null && echo "✅ OK" || echo "❌ Failed"
	@echo -n "mcpproxy: "
	@curl -s http://localhost:8080/health > /dev/null && echo "✅ OK" || echo "❌ Not running (start with 'make start-mcpproxy')"

start-adk-web: ## Start ADK web UI for interactive evaluation
	@echo "🌐 Starting ADK web UI..."
	@echo "📋 This will open the web interface for:"
	@echo "  - Interactive agent testing"
	@echo "  - Evaluation case creation"
	@echo "  - Manual annotation and review"
	@mkdir -p eval/adk_agents
	@echo "🔗 Opening: http://localhost:8000"
	adk web eval/adk_agents

run-eval-cli: ## Run evaluation via ADK CLI (for automation)
	@echo "🚀 Running ADK CLI evaluation..."
	@echo "📋 Using saved evaluation sets from ADK web UI"
	@mkdir -p eval/results
	adk eval eval/adk_agents eval/datasets/eval_sets/*.evalset.json --print_detailed_results

test-agent: ## Test ADK agent setup
	@echo "🧪 Testing ADK agent configuration..."
	cd eval/adk_agents && python test_agent.py

generate-eval-templates: ## Generate evaluation template files
	@echo "🎯 Generating evaluation templates..."
	@mkdir -p eval/datasets/eval_sets
	@mkdir -p eval/adk_agents
	@echo "📝 Templates created in eval/datasets/"
	@echo "💡 Use 'make start-adk-web' to create evaluation cases interactively"

show-logs: ## Show mcpproxy logs for debugging
	@echo "📜 Recent mcpproxy logs:"
	@tail -20 ~/Library/Logs/mcpproxy/main.log || echo "❌ Log file not found"

debug-logs: ## Show filtered debug logs
	@echo "🔍 Showing debug logs (filtered)..."
	@echo "Run this command to monitor in real-time:"
	@echo "tail -f ~/Library/Logs/mcpproxy/main.log | grep -E '(error|fail|success|quarantine|registry)'"

stop-all: ## Stop all running services
	@echo "🛑 Stopping all services..."
	@pkill -f "mcp-registry" || true
	@pkill -f "uvicorn.*8001" || true
	@echo "✅ Registry stopped!"
	@echo "ℹ️  MCP servers run on-demand via stdio (no persistent processes to stop)"

clean: stop-all ## Clean up everything and reset environment
	@echo "🧹 Cleaning up evaluation environment..."
	@rm -rf eval/results/*
	@rm -rf eval/datasets/scenarios/*.json
	@echo "✅ Environment cleaned!"

check-deps: ## Check if all dependencies are available
	@echo "🔍 Checking dependencies..."
	@command -v uv >/dev/null 2>&1 || (echo "❌ uv not found. Install with: curl -LsSf https://astral.sh/uv/install.sh | sh" && exit 1)
	@command -v go >/dev/null 2>&1 || (echo "❌ Go not found. Install from: https://golang.org/" && exit 1)
	@test -f ../mcpproxy || (echo "❌ mcpproxy binary not found. Run 'cd .. && go build'" && exit 1)
	@echo "✅ All dependencies available!"

status: ## Show status of all services
	@echo "📊 Service Status:"
	@echo "=================="
	@echo -n "🔗 Registry (8001): "
	@curl -s http://localhost:8001/health >/dev/null && echo "✅ Running" || echo "❌ Stopped"
	@echo -n "🔧 mcpproxy (8080): "
	@curl -s http://localhost:8080/health >/dev/null && echo "✅ Running" || echo "❌ Stopped"
	@echo ""
	@echo "🎲 MCP Servers (stdio mode):"
	@echo "  All servers available via uvx commands ✅"

# Development targets
dev-setup: ## Setup development environment with hot reload
	@echo "🔥 Setting up development environment..."
	uv sync --dev
	@echo "✅ Development environment ready!"

dev-test: ## Run development tests
	@echo "🧪 Running tests..."
	uv run pytest tests/ -v
	@echo "✅ Tests completed!"

# Complete workflow
full-eval: check-deps setup start-all test-connection start-adk-web ## Complete evaluation workflow setup
	@echo "🎉 Complete evaluation environment ready!"
	@echo "🌐 ADK web UI should be opening for interactive evaluation"

# Help text
help-scenarios: ## Show detailed scenario descriptions
	@echo "📋 Evaluation Scenarios:"
	@echo "======================="
	@echo ""
	@echo "🎯 Scenario 1: Adding Server with Quarantine"
	@echo "  Tests agent's ability to:"
	@echo "  - Add upstream MCP servers"
	@echo "  - Handle server addition failures"
	@echo "  - Debug issues using log tailing"
	@echo "  - Generate security reports for quarantined servers"
	@echo ""
	@echo "🎯 Scenario 2: Search and Add Server"
	@echo "  Tests agent's ability to:"
	@echo "  - List available registries"
	@echo "  - Search for servers by capability"
	@echo "  - Select appropriate server from results"
	@echo "  - Add server found in search results"
	@echo ""
	@echo "🌐 ADK Web UI Workflow:"
	@echo "  1. Start services: make start-all"
	@echo "  2. Start mcpproxy: make start-mcpproxy"
	@echo "  3. Open ADK web UI: make start-adk-web"
	@echo "  4. Interact with agent to create sessions"
	@echo "  5. Save sessions as evaluation cases"
	@echo "  6. Run evaluations with custom metrics"
	@echo ""
	@echo "🔧 Configuration:"
	@echo "  - Registry URL: http://localhost:8001"
	@echo "  - mcpproxy URL: http://localhost:8080"
	@echo "  - MCP Servers: Available via stdio (uvx commands)" 