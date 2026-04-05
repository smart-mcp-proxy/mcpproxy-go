package scanner

// bundledScanners contains the default scanner registry entries
// These are well-known MCP security scanners that ship with MCPProxy
var bundledScanners = []*ScannerPlugin{
	{
		ID:          "mcp-scan",
		Name:        "MCP Scan",
		Vendor:      "Invariant Labs",
		Description: "Detects tool poisoning attacks, prompt injection, cross-origin escalation, and rug pulls in MCP servers.",
		License:     "MIT",
		Homepage:    "https://github.com/invariantlabs-ai/mcp-scan",
		DockerImage: "mcpproxy/scanner-mcp-scan:latest",
		Inputs:      []string{"source"},
		Outputs:     []string{"sarif"},
		RequiredEnv: nil,
		OptionalEnv: nil,
		Command:     []string{"mcp-scan", "--json"},
		Timeout:     "120s",
		NetworkReq:  true, // Needs network for Invariant API
	},
	{
		ID:          "cisco-mcp-scanner",
		Name:        "Cisco MCP Scanner",
		Vendor:      "Cisco AI Defense",
		Description: "YARA rules + readiness analysis. Detects tool poisoning, prompt injection, credential harvesting, and data exfiltration. No API key needed for offline mode.",
		License:     "Apache-2.0",
		Homepage:    "https://github.com/cisco-ai-defense/mcp-scanner",
		DockerImage: "mcpproxy/scanner-cisco:latest",
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
