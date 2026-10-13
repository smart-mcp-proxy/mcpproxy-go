package telemetry

import (
	"encoding/json"
	"testing"
)

// Spec 102 — the heartbeat must carry BOTH serialization axes, not just
// routing_mode.
//
// routing_mode says which tool surface an install serves. It says nothing about
// whether the operator ever turned on the compact or deferred rendering that the
// entire token-reduction effort exists to deliver, so on its own it cannot
// answer "is anyone using this?" — the same structural blind spot that made TPA
// adoption look like rejection when it was really "never switched on".
func TestPayload_CarriesBothSerializationAxes(t *testing.T) {
	payload := HeartbeatPayload{
		RoutingMode:            "direct",
		ToolResponseMode:       "compact",
		DirectToolResponseMode: "deferred",
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for key, want := range map[string]string{
		"routing_mode":              "direct",
		"tool_response_mode":        "compact",
		"direct_tool_response_mode": "deferred",
	} {
		got, ok := wire[key]
		if !ok {
			t.Errorf("%s missing from the heartbeat wire payload", key)
			continue
		}
		if got != want {
			t.Errorf("%s = %v, want %q", key, got, want)
		}
	}
}

// The three axes are independent: reporting one must never be derived from
// another. A payload that reported direct serialization only when routing_mode
// happened to be "direct" would under-count /mcp/all, which is direct-serving
// regardless of routing_mode.
func TestPayload_SerializationAxesAreIndependentOfRoutingMode(t *testing.T) {
	payload := HeartbeatPayload{
		RoutingMode:            "retrieve_tools",
		DirectToolResponseMode: "deferred",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if wire["direct_tool_response_mode"] != "deferred" {
		t.Errorf("direct_tool_response_mode must be reported regardless of routing_mode; got %v",
			wire["direct_tool_response_mode"])
	}
}
