package headerfwd

import "testing"

// Spec 124 FR-008: the run name is a label for MCPProxy's own activity log and
// must never reach an upstream server, whatever the forwarding allow-list
// says. The header name is spelled out here on purpose: this package must not
// import reqcontext, and a rename there has to fail this test.
func TestRunNameHeaderIsNeverForwarded(t *testing.T) {
	for _, name := range []string{"X-MCPProxy-Run", "x-mcpproxy-run", " X-Mcpproxy-Run "} {
		if !Denied(name) {
			t.Errorf("%q must be denied for forwarding", name)
		}
	}
}
