package telemetry

import "testing"

// describe_tool called recordBuiltinTool from the day it shipped (spec 085) but
// was missing from builtinToolAllowList, so RecordBuiltinTool's unknown-name
// early return dropped every call and the counter always read zero.
//
// This is the counter spec 102 depends on most: describe_tool is how an agent
// redeems a lossy compact signature, so "is the escape hatch actually used?" is
// the question that decides whether deferral is working — and an always-zero
// counter answers it wrongly, in the direction that looks like non-adoption.
func TestBuiltinAllowList_CoversEveryToolThatRecords(t *testing.T) {
	// Every name the server passes to recordBuiltinTool. Keep in step with the
	// recordBuiltinTool call sites in internal/server.
	for _, name := range []string{
		"retrieve_tools",
		"describe_tool",
		"call_tool_read",
		"call_tool_write",
		"call_tool_destructive",
		"upstream_servers",
		"quarantine_security",
		"code_execution",
	} {
		if !IsBuiltinTool(name) {
			t.Errorf("%q records a builtin-tool call but is not in the allow list, so every call is dropped", name)
		}
	}
}

// The allow list is also a leak guard: an upstream tool name must never reach
// the heartbeat.
func TestBuiltinAllowList_StillRejectsUpstreamNames(t *testing.T) {
	r := NewCounterRegistry()
	r.RecordBuiltinTool("describe_tool")
	r.RecordBuiltinTool("github:create_issue")
	r.RecordBuiltinTool("some_upstream_tool")

	counts := r.Snapshot().BuiltinToolCalls
	if counts["describe_tool"] != 1 {
		t.Errorf("describe_tool = %d, want 1", counts["describe_tool"])
	}
	for name := range counts {
		if !IsBuiltinTool(name) {
			t.Errorf("upstream name %q leaked into the heartbeat", name)
		}
	}
}
