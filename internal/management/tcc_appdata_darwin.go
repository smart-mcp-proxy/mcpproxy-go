//go:build darwin

package management

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/connect"

// appDataDenialWarning probes the real macOS MCP-client configs for a persisted
// App-Data (TCC) denial and returns an actionable doctor runtime warning when one
// is found (Spec 075 US3, T023). It reads at most one installed client config
// (the explicit-action doctor path); GetAllStatus remains content-read-free.
func appDataDenialWarning() (string, bool) {
	return appDataWarningFrom(connect.NewService("", ""))
}
