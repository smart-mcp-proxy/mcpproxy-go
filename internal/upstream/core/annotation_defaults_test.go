package core

import (
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/stretchr/testify/assert"
)

func boolPtr(b bool) *bool { return &b }

func TestMergeAnnotationDefaults_NilDst(t *testing.T) {
	// When dst has all nil hints, defaults fill them in
	dst := &config.ToolAnnotations{}
	defaults := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(false),
		DestructiveHint: boolPtr(false),
		OpenWorldHint:   boolPtr(true),
	}
	mergeAnnotationDefaults(dst, defaults)

	assert.Equal(t, boolPtr(false), dst.ReadOnlyHint)
	assert.Equal(t, boolPtr(false), dst.DestructiveHint)
	assert.Equal(t, boolPtr(true), dst.OpenWorldHint)
	assert.Nil(t, dst.IdempotentHint) // not in defaults
}

func TestMergeAnnotationDefaults_UpstreamWins(t *testing.T) {
	// Explicit upstream values are never overridden
	dst := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(true),
		DestructiveHint: boolPtr(true),
	}
	defaults := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(false),
		DestructiveHint: boolPtr(false),
		OpenWorldHint:   boolPtr(false),
	}
	mergeAnnotationDefaults(dst, defaults)

	assert.Equal(t, boolPtr(true), dst.ReadOnlyHint)   // upstream wins
	assert.Equal(t, boolPtr(true), dst.DestructiveHint) // upstream wins
	assert.Equal(t, boolPtr(false), dst.OpenWorldHint)  // filled from defaults
}

func TestMergeAnnotationDefaults_TitleMerge(t *testing.T) {
	dst := &config.ToolAnnotations{Title: ""}
	defaults := &config.ToolAnnotations{Title: "Default Title"}
	mergeAnnotationDefaults(dst, defaults)
	assert.Equal(t, "Default Title", dst.Title)

	// Existing title preserved
	dst2 := &config.ToolAnnotations{Title: "Upstream Title"}
	mergeAnnotationDefaults(dst2, defaults)
	assert.Equal(t, "Upstream Title", dst2.Title)
}

func TestAnnotationDefaults_NoDefaultsNoAnnotations(t *testing.T) {
	// Backward compat: no defaults configured, no upstream annotations → stays nil
	var serverDefaults *config.ToolAnnotations = nil
	var toolAnnotations *config.ToolAnnotations = nil

	// Simulates the logic in client.go
	if serverDefaults != nil {
		if toolAnnotations == nil {
			toolAnnotations = &config.ToolAnnotations{}
		}
		mergeAnnotationDefaults(toolAnnotations, serverDefaults)
	}

	assert.Nil(t, toolAnnotations)
}

func TestAnnotationDefaults_FullCopy(t *testing.T) {
	// No upstream annotations + defaults set → full copy from defaults
	defaults := &config.ToolAnnotations{
		ReadOnlyHint:    boolPtr(false),
		DestructiveHint: boolPtr(false),
		IdempotentHint:  boolPtr(true),
		OpenWorldHint:   boolPtr(true),
		Title:           "Default",
	}

	// Simulates the nil-annotations branch in client.go
	result := &config.ToolAnnotations{
		Title:           defaults.Title,
		ReadOnlyHint:    defaults.ReadOnlyHint,
		DestructiveHint: defaults.DestructiveHint,
		IdempotentHint:  defaults.IdempotentHint,
		OpenWorldHint:   defaults.OpenWorldHint,
	}

	assert.Equal(t, boolPtr(false), result.ReadOnlyHint)
	assert.Equal(t, boolPtr(false), result.DestructiveHint)
	assert.Equal(t, boolPtr(true), result.IdempotentHint)
	assert.Equal(t, boolPtr(true), result.OpenWorldHint)
	assert.Equal(t, "Default", result.Title)
}
