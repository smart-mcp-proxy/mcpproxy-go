package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSignatureBundleLinesZeroRunnableWarns: a corpus with zero runnable rules
// means TPA coverage is OFF. Rendering a bare "0 runnable" in the same tone as
// a healthy count let an operator read a switched-off scanner as a working one.
func TestSignatureBundleLinesZeroRunnableWarns(t *testing.T) {
	lines := signatureBundleLines(map[string]interface{}{
		"signature_bundle": map[string]interface{}{
			"source":         "file",
			"path":           "/opt/tpa/scanner-bundle.json",
			"bundle_version": "0.1.0",
			"runnable_rules": float64(0),
			"skipped_rules":  float64(4),
		},
	})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "WARNING", "zero runnable signatures must not render in a normal tone")
	assert.Contains(t, strings.ToLower(joined), "no tpa signatures")
}
