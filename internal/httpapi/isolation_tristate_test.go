package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNullableBool_AbsentNullValue pins the three states the PATCH seam needs
// to express for a tri-state override (GH #1142): absent = leave alone,
// null = clear back to "inherit global", true/false = set explicitly.
func TestNullableBool_AbsentNullValue(t *testing.T) {
	type body struct {
		Enabled NullableBool `json:"enabled"`
	}

	tests := []struct {
		name      string
		raw       string
		wantSet   bool
		wantValue *bool
	}{
		{name: "absent", raw: `{}`, wantSet: false, wantValue: nil},
		{name: "null clears the override", raw: `{"enabled":null}`, wantSet: true, wantValue: nil},
		{name: "explicit true", raw: `{"enabled":true}`, wantSet: true, wantValue: config.BoolPtr(true)},
		{name: "explicit false", raw: `{"enabled":false}`, wantSet: true, wantValue: config.BoolPtr(false)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b body
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &b))
			assert.Equal(t, tt.wantSet, b.Enabled.Set, "Set")
			if tt.wantValue == nil {
				assert.Nil(t, b.Enabled.Value, "Value")
			} else {
				require.NotNil(t, b.Enabled.Value)
				assert.Equal(t, *tt.wantValue, *b.Enabled.Value, "Value")
			}
		})
	}
}

// TestIsolationRequest_ResolvePreservesUnexposed proves that resolving a patch
// against the persisted overrides keeps the fields IsolationRequest does not
// expose (mode, log driver/size/files) instead of dropping them.
func TestIsolationRequest_ResolvePreservesUnexposed(t *testing.T) {
	sandbox := config.IsolationModeSandbox
	existing := &config.IsolationConfig{
		Enabled:     config.BoolPtr(true),
		Mode:        &sandbox,
		Image:       "old-image",
		LogDriver:   "local",
		LogMaxSize:  "10m",
		LogMaxFiles: "5",
	}
	newImage := "new-image"
	req := &IsolationRequest{Image: &newImage}

	out := req.resolve(existing)
	require.NotNil(t, out)
	assert.Equal(t, "new-image", out.Image)
	require.NotNil(t, out.Enabled, "an untouched enabled override must survive")
	assert.True(t, *out.Enabled)
	require.NotNil(t, out.Mode)
	assert.Equal(t, sandbox, *out.Mode)
	assert.Equal(t, "local", out.LogDriver)
	assert.Equal(t, "10m", out.LogMaxSize)
	assert.Equal(t, "5", out.LogMaxFiles)
	// The result must not alias the persisted config.
	assert.NotSame(t, existing, out)
	assert.NotSame(t, existing.Enabled, out.Enabled)
}

// TestIsolationRequest_ResolveClearsOverride pins the "back to inherit" path.
func TestIsolationRequest_ResolveClearsOverride(t *testing.T) {
	existing := &config.IsolationConfig{Enabled: config.BoolPtr(false), Image: "img"}
	req := &IsolationRequest{EnabledOverride: NullableBool{Set: true, Value: nil}}

	out := req.resolve(existing)
	require.NotNil(t, out)
	assert.Nil(t, out.Enabled, "an explicit null must clear the override back to inherit")
	assert.Equal(t, "img", out.Image, "unrelated fields survive")
}

// TestIsolationRequest_ResolveSetsExplicit covers the ordinary set path.
func TestIsolationRequest_ResolveSetsExplicit(t *testing.T) {
	req := &IsolationRequest{EnabledOverride: NullableBool{Set: true, Value: config.BoolPtr(false)}}
	out := req.resolve(nil)
	require.NotNil(t, out)
	require.NotNil(t, out.Enabled)
	assert.False(t, *out.Enabled)
}
