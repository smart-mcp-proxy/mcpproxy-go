package httpapi

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsolationRequestToConfigNil(t *testing.T) {
	var r *IsolationRequest
	assert.Nil(t, r.toConfig(), "nil request must produce nil config")
}

func TestIsolationRequestToConfigEmpty(t *testing.T) {
	// An empty-but-present IsolationRequest means "I am touching
	// isolation but setting nothing explicitly" — toConfig returns a
	// non-nil struct so the update path can distinguish this from nil.
	r := &IsolationRequest{}
	got := r.toConfig()
	require.NotNil(t, got)
	assert.Nil(t, got.Enabled)
	assert.Empty(t, got.Image)
	assert.Empty(t, got.NetworkMode)
	assert.Empty(t, got.ExtraArgs)
	assert.Empty(t, got.WorkingDir)
}

func TestIsolationRequestToConfigAllFields(t *testing.T) {
	enabled := true
	image := "python:3.11"
	networkMode := "bridge"
	extra := []string{"-v", "/host:/container:rw"}
	workingDir := "/vault"

	r := &IsolationRequest{
		Enabled:     &enabled,
		Image:       &image,
		NetworkMode: &networkMode,
		ExtraArgs:   &extra,
		WorkingDir:  &workingDir,
	}
	got := r.toConfig()
	require.NotNil(t, got)
	require.NotNil(t, got.Enabled)
	assert.True(t, *got.Enabled)
	assert.Equal(t, image, got.Image)
	assert.Equal(t, networkMode, got.NetworkMode)
	assert.Equal(t, extra, got.ExtraArgs)
	assert.Equal(t, workingDir, got.WorkingDir)
}

func TestIsolationRequestToConfigDisabledExplicitly(t *testing.T) {
	// A present enabled:false must produce a pointer to false, not nil
	// (nil means "do not touch"; false means "set to false").
	enabled := false
	r := &IsolationRequest{Enabled: &enabled}
	got := r.toConfig()
	require.NotNil(t, got)
	require.NotNil(t, got.Enabled)
	assert.False(t, *got.Enabled)
}

func TestIsolationRequestToConfigExtraArgsCopies(t *testing.T) {
	// The resulting slice must not alias the request slice, so later
	// mutations on the config do not leak back into the request
	// (matters when the request gets held in memory by a log sink).
	src := []string{"-v", "/foo:/bar"}
	r := &IsolationRequest{ExtraArgs: &src}
	got := r.toConfig()
	require.NotNil(t, got)
	got.ExtraArgs[0] = "mutated"
	assert.Equal(t, "-v", src[0], "request slice must remain untouched after mutating config copy")
}

// Compile-time assertion that toConfig returns *config.IsolationConfig
// (not contracts.IsolationConfig). Lives outside a test function so
// static analysers don't flag the unused LHS inside a test body.
var _ func(*IsolationRequest) *config.IsolationConfig = (*IsolationRequest).toConfig
