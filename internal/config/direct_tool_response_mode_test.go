package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec 102 (schema-deferred direct mode), T007.
//
// direct_tool_response_mode is a DEDICATED axis, resolved in plan.md D1. The
// rejected alternative was to let the existing global tool_response_mode govern
// the direct surface too — cheaper in config, but it would silently change
// /mcp/all output for every deployment already running compact, which FR-015
// forbids. These tests pin both halves of that decision: the new key behaves,
// and the existing one is untouched.

func fieldError(t *testing.T, c *Config, field string) (ValidationError, bool) {
	t.Helper()
	for _, e := range c.ValidateDetailed() {
		if e.Field == field {
			return e, true
		}
	}
	return ValidationError{}, false
}

func TestDirectToolResponseMode_DefaultIsFull(t *testing.T) {
	c := &Config{}
	if c.DirectToolResponseMode != "" {
		t.Fatalf("zero value should be empty (= full), got %q", c.DirectToolResponseMode)
	}
	if _, found := fieldError(t, c, "direct_tool_response_mode"); found {
		t.Error("an empty value must be valid and resolve to full (FR-001: default-off)")
	}
}

func TestDirectToolResponseMode_AcceptsFullAndDeferred(t *testing.T) {
	for _, mode := range []string{DirectToolResponseModeFull, DirectToolResponseModeDeferred} {
		c := &Config{DirectToolResponseMode: mode}
		if _, found := fieldError(t, c, "direct_tool_response_mode"); found {
			t.Errorf("mode %q must be accepted", mode)
		}
	}
}

func TestDirectToolResponseMode_UnknownValueNamesBothAcceptedValues(t *testing.T) {
	c := &Config{DirectToolResponseMode: "compact"} // a real value — of the OTHER axis
	e, found := fieldError(t, c, "direct_tool_response_mode")
	if !found {
		t.Fatal("an unknown value must be rejected")
	}
	// "compact" is the likeliest wrong value precisely because it is valid on
	// tool_response_mode, so the message has to name what IS accepted here.
	for _, want := range []string{"full", "deferred"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message must name the accepted value %q; got %q", want, e.Message)
		}
	}
}

// TestRoutingMode_SchemaDeferredRejectedWithComposition covers FR-002: users
// arriving from issue #971's proposed config write routing_mode:"schema_deferred".
// That value must fail in a way that teaches the supported composition rather
// than just listing the three legal modes.
func TestRoutingMode_SchemaDeferredRejectedWithComposition(t *testing.T) {
	c := &Config{RoutingMode: "schema_deferred"}
	e, found := fieldError(t, c, "routing_mode")
	if !found {
		t.Fatal("routing_mode schema_deferred must be rejected")
	}
	for _, want := range []string{"direct", "direct_tool_response_mode", "deferred"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("message must name the supported composition (%q missing); got %q", want, e.Message)
		}
	}
}

// TestRoutingMode_OtherInvalidValuesKeepTheGenericMessage guards against the
// composition hint swallowing the ordinary case.
func TestRoutingMode_OtherInvalidValuesKeepTheGenericMessage(t *testing.T) {
	c := &Config{RoutingMode: "banana"}
	e, found := fieldError(t, c, "routing_mode")
	if !found {
		t.Fatal("an unknown routing mode must be rejected")
	}
	if strings.Contains(e.Message, "direct_tool_response_mode") {
		t.Errorf("only schema_deferred should get the composition hint; got %q", e.Message)
	}
}

// TestDirectToolResponseMode_IsOrthogonalToToolResponseMode pins the D1
// decision itself: the two axes are independent, and compact + deferred is a
// legal combination in which each governs only its own surface.
func TestDirectToolResponseMode_IsOrthogonalToToolResponseMode(t *testing.T) {
	c := &Config{
		ToolResponseMode:       ToolResponseModeCompact,
		DirectToolResponseMode: DirectToolResponseModeDeferred,
	}
	for _, f := range []string{"tool_response_mode", "direct_tool_response_mode"} {
		if _, found := fieldError(t, c, f); found {
			t.Errorf("compact + deferred must be legal; %s was rejected", f)
		}
	}
}

// TestDirectToolResponseMode_JSONKey pins the wire name operators write.
func TestDirectToolResponseMode_JSONKey(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"direct_tool_response_mode":"deferred"}`), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.DirectToolResponseMode != DirectToolResponseModeDeferred {
		t.Errorf("direct_tool_response_mode did not bind; got %q", c.DirectToolResponseMode)
	}

	// omitempty: an unset axis must not appear in a written config, or every
	// existing config file grows a key its owner never chose.
	out, err := json.Marshal(&Config{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "direct_tool_response_mode") {
		t.Error("an unset direct_tool_response_mode must be omitted from serialized config")
	}
}

// TestDirectToolResponseModeEnvOverride is T010: the MCPPROXY_DIRECT_TOOL_
// RESPONSE_MODE alias behaves like its retrieve_tools sibling, and — the part
// that matters for D1 — the two aliases are independent. Setting one must never
// move the other, or the "dedicated axis" decision is only skin deep.
func TestDirectToolResponseModeEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "mcp_config.json")
	raw, err := json.Marshal(map[string]any{
		"listen":                    "127.0.0.1:0",
		"data_dir":                  tmp,
		"direct_tool_response_mode": DirectToolResponseModeFull,
		"tool_response_mode":        ToolResponseModeFull,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(cfgPath, raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("env wins over file", func(t *testing.T) {
		t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", DirectToolResponseModeDeferred)
		cfg, err := LoadFromFile(cfgPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.DirectToolResponseMode != DirectToolResponseModeDeferred {
			t.Errorf("env override did not apply; got %q", cfg.DirectToolResponseMode)
		}
	})

	t.Run("no env keeps file value", func(t *testing.T) {
		cfg, err := LoadFromFile(cfgPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.DirectToolResponseMode != DirectToolResponseModeFull {
			t.Errorf("file value lost; got %q", cfg.DirectToolResponseMode)
		}
	})

	t.Run("invalid env value fails validation", func(t *testing.T) {
		t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", "bogus")
		_, err := LoadFromFile(cfgPath)
		if err == nil {
			t.Fatal("an invalid env value must fail validation")
		}
		if !strings.Contains(err.Error(), "direct_tool_response_mode") {
			t.Errorf("error must name the field; got %v", err)
		}
	})

	t.Run("the two axes are independent", func(t *testing.T) {
		t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", DirectToolResponseModeDeferred)
		cfg, err := LoadFromFile(cfgPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.ToolResponseMode != ToolResponseModeFull {
			t.Errorf("setting the direct alias moved tool_response_mode to %q", cfg.ToolResponseMode)
		}

		t.Setenv("MCPPROXY_DIRECT_TOOL_RESPONSE_MODE", "")
		t.Setenv("MCPPROXY_TOOL_RESPONSE_MODE", ToolResponseModeCompact)
		cfg, err = LoadFromFile(cfgPath)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if cfg.DirectToolResponseMode != DirectToolResponseModeFull {
			t.Errorf("setting the retrieve_tools alias moved direct_tool_response_mode to %q", cfg.DirectToolResponseMode)
		}
	})
}
