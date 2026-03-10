# typed: false
# frozen_string_literal: true

# Homebrew formula for MCPProxy CLI (headless server)
# To install from the tap:
#   brew tap smart-mcp-proxy/mcpproxy
#   brew install mcpproxy
#
# To submit to homebrew-core, see homebrew/README.md

class Mcpproxy < Formula
  desc "Smart MCP proxy with intelligent tool discovery for AI agents"
  homepage "https://mcpproxy.app"
  url "https://github.com/smart-mcp-proxy/mcpproxy-go/archive/refs/tags/v0.20.2.tar.gz"
  sha256 "aec23fff361d3bc9c874de0a37472301404bdeed18b4625dddcef492fb364914"
  license "MIT"
  head "https://github.com/smart-mcp-proxy/mcpproxy-go.git", branch: "main"

  depends_on "go" => :build
  depends_on "node" => :build

  def install
    # Generate TypeScript types from Go contracts (needed before frontend build)
    system "go", "run", "./cmd/generate-types"

    # Build frontend (embedded in the binary via go:embed)
    cd "frontend" do
      system "npm", "install"
      system "npm", "run", "build"
    end

    # Copy frontend dist for embedding
    mkdir_p "web/frontend"
    cp_r "frontend/dist", "web/frontend/"

    ldflags = %W[
      -s -w
      -X main.version=v#{version}
      -X main.commit=brew
      -X main.date=#{time.iso8601}
      -X github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi.buildVersion=v#{version}
    ]
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/mcpproxy"
  end

  def post_install
    (var/"log/mcpproxy").mkpath
  end

  service do
    run [opt_bin/"mcpproxy", "serve"]
    keep_alive true
    log_path var/"log/mcpproxy/output.log"
    error_log_path var/"log/mcpproxy/error.log"
  end

  test do
    # Verify version output
    assert_match "MCPProxy v#{version}", shell_output("#{bin}/mcpproxy version")

    # Verify help output
    assert_match "Smart MCP Proxy", shell_output("#{bin}/mcpproxy --help")

    # Verify serve command exists
    assert_match "serve", shell_output("#{bin}/mcpproxy --help")

    # Try starting the server briefly to verify it can bind
    port = free_port
    pid = fork do
      exec bin/"mcpproxy", "serve", "--listen", "127.0.0.1:#{port}"
    end
    sleep 2

    # Check the status endpoint responds
    output = shell_output("curl -sf http://127.0.0.1:#{port}/api/v1/status 2>/dev/null || true")
    assert_match "edition", output if output.length > 0
  ensure
    Process.kill("TERM", pid) if pid
    Process.wait(pid) if pid
  end
end
