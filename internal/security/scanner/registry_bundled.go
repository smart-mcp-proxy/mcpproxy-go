package scanner

// bundledScanners contains the default scanner registry entries.
// These are well-known MCP security scanners that ship with MCPProxy.
//
// Image source policy:
//   - Prefer the vendor's official published image when one exists
//     (e.g. semgrep, trivy).
//   - When no vendor image exists we publish our own thin wrapper to
//     `ghcr.io/smart-mcp-proxy/scanner-<id>:latest`. The Dockerfiles and
//     the publishing workflow live in `docker/scanners/`. See
//     `docs/features/scanner-images.md` for the rationale behind keeping
//     the scanner images in this repo instead of a separate one.
//
// Keep this slice sorted alphabetically by ID so the list order is
// deterministic across API, CLI, and UI.
var bundledScanners = []*ScannerPlugin{
	{
		ID:          "cisco-mcp-scanner",
		Name:        "Cisco MCP Scanner",
		Vendor:      "Cisco AI Defense",
		Description: "YARA rules + readiness analysis. Detects tool poisoning, prompt injection, credential harvesting, and data exfiltration. No API key needed for offline mode.",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/cisco-ai-defense/mcp-scanner",
		DockerImage: "ghcr.io/smart-mcp-proxy/scanner-cisco:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil, // YARA + readiness work without any API key
		OptionalEnv: []EnvRequirement{
			{Key: "MCP_SCANNER_API_KEY", Label: "Cisco AI Defense API Key (for cloud analysis)", Secret: true},
			{Key: "VIRUSTOTAL_API_KEY", Label: "VirusTotal API Key", Secret: true},
		},
		Command:    []string{"--analyzers", "yara,readiness", "--format", "raw", "static", "--tools", "/scan/source/tools.json"},
		Timeout:    "120s",
		NetworkReq: false, // YARA + readiness are fully offline
	},
	{
		ID:          "mcp-ai-scanner",
		Name:        "MCP AI Scanner",
		Vendor:      "MCPProxy",
		Description: "AI-powered MCP security scanner using Claude Agent SDK. Agent explores code with Read/Grep/Glob tools like a security specialist. Detects tool poisoning, prompt injection, data exfiltration, and malicious code. Pattern scan works offline; AI analysis needs API key or OAuth token. Note: AI analysis may produce false positives — always verify findings manually. You can use your Claude Code subscription tokens for this scanner.",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/smart-mcp-proxy/mcp-scanner",
		DockerImage: "ghcr.io/smart-mcp-proxy/mcp-scanner:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: []EnvRequirement{
			{Key: "ANTHROPIC_API_KEY", Label: "Anthropic API key for AI analysis (pattern scan works without it)", Secret: true},
			{Key: "CLAUDE_CODE_OAUTH_TOKEN", Label: "Claude Code OAuth token for AI analysis (alternative to API key)", Secret: true},
			{Key: "SCANNER_MODEL", Label: "Claude model for AI analysis (default: claude-sonnet-4-6)", Secret: false},
		},
		Command:    nil,    // Uses entrypoint.py
		Timeout:    "900s", // 15 minutes — AI analysis can take time on large codebases
		NetworkReq: true,   // AI analysis needs network for Claude API
	},
	{
		ID:          "mcp-scan",
		Name:        "Snyk Agent Scan",
		Vendor:      "Snyk (Invariant Labs)",
		Description: "Detects tool poisoning, prompt injection, tool shadowing, toxic flows, secrets exposure, and rug pulls. Requires free Snyk token.",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/snyk/agent-scan",
		DockerImage: "ghcr.io/smart-mcp-proxy/scanner-snyk:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: []EnvRequirement{
			{Key: "SNYK_TOKEN", Label: "Snyk API Token (free at app.snyk.io → Account Settings → API Token)", Secret: true},
		},
		OptionalEnv: nil,
		Command:     nil, // Uses entrypoint.sh
		Timeout:     "120s",
		NetworkReq:  true, // Sends tool descriptions to Snyk API for analysis
	},
	{
		ID:          "nova-proximity",
		Name:        "Nova Proximity",
		Vendor:      "MCPProxy (NOVA-inspired rules)",
		Description: "Keyword-based MCP security scanner. Detects prompt injection, tool poisoning, data exfiltration, credential harvesting, dangerous commands, and impersonation. Fully offline — no API key needed.",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/fr0gger/proximity",
		DockerImage: "ghcr.io/smart-mcp-proxy/scanner-proximity:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: nil,
		Command:     nil, // Uses entrypoint.py
		Timeout:     "60s",
		NetworkReq:  false, // Fully offline keyword matching
	},
	{
		ID:          "ramparts",
		Name:        "Ramparts MCP Scanner",
		Vendor:      "Javelin (getjavelin.com)",
		Description: "Rust-based MCP security scanner with YARA rules. Detects tool poisoning, SQL injection, command injection, path traversal, secrets leakage, and prompt injection.",
		License:     "Proprietary",
		Homepage:    "https://github.com/getjavelin/ramparts",
		DockerImage: "ghcr.io/smart-mcp-proxy/scanner-ramparts:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: nil,
		Command:     nil, // Uses entrypoint.sh
		Timeout:     "120s",
		NetworkReq:  true, // Stub MCP server runs inside container
	},
	{
		ID:          "semgrep-mcp",
		Name:        "Semgrep MCP Rules",
		Vendor:      "Semgrep",
		Description: "Static analysis for detecting dangerous code patterns, injection vectors, and hardcoded secrets in server source code.",
		License:     "LGPL-2.1",
		Homepage:    "https://semgrep.dev",
		DockerImage: "returntocorp/semgrep:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: []EnvRequirement{
			{Key: "SEMGREP_APP_TOKEN", Label: "Semgrep Cloud Token", Secret: true},
		},
		Command:    []string{"semgrep", "scan", "--novcs", "--sarif", "--output", "/scan/report/results.sarif", "--exclude", "site-packages", "--exclude", "node_modules", "--exclude", "dist-packages", "/scan/source"},
		Timeout:    "600s", // 10 minutes — large source trees take time
		NetworkReq: true,   // Downloads rules from registry
	},
	{
		ID:          "trivy-mcp",
		Name:        "Trivy Vulnerability Scanner",
		Vendor:      "Aqua Security",
		Description: "Comprehensive vulnerability scanner for filesystem, dependencies, and container images. Detects known CVEs and misconfigurations.",
		License:     "Apache-2.0",
		Homepage:    "https://trivy.dev",
		DockerImage: "ghcr.io/aquasecurity/trivy:latest",
		Inputs:      []string{"source", "container_image"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: nil,
		Command:     []string{"fs", "--format", "sarif", "/scan/source"},
		Timeout:     "300s", // First run downloads vuln DB (~90MB)
		NetworkReq:  true,   // Needs to download vulnerability database
	},
}
