// Command overrideprobe is not part of the module's build graph (it lives
// under a "testdata" directory, which `go build ./...`/`go vet ./...`
// ignore). It exists solely for
// TestEnablePolicyForTest_PanicsOutsideATestBinary
// (internal/config/profiles_rollout_gate_test.go), which builds it
// explicitly with `go build` and runs the resulting binary to prove
// config.EnablePolicyForTest panics outside a test binary — the FR-009a
// invariant that the rollout-gate override is unreachable from production
// code.
package main

import "github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

func main() {
	config.EnablePolicyForTest(nil)
}
