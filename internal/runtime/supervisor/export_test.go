package supervisor

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/configsvc"

// ExecuteActionForTest runs one planned action synchronously, as the
// reconcile loop's goroutine would after being delayed.
func (s *Supervisor) ExecuteActionForTest(name string, action ReconcileAction, snap *configsvc.Snapshot) error {
	return s.executeAction(name, action, snap)
}
