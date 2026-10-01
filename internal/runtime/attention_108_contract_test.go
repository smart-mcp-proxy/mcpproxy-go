package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attentionKindConstName maps a kind value to its generated TypeScript
// constant name: sign_in_required -> AttentionKindSignInRequired.
func attentionKindConstName(kind string) string {
	var b strings.Builder
	b.WriteString("AttentionKind")
	for _, w := range strings.Split(kind, "_") {
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// TestAttention108KindsMatchGeneratedTypeScript pins the Go kinds against the
// generated frontend contract with an EXACT line per constant (never a
// substring match, #1376 follow-up 5). Spec 109-l P13; the 109-m parity test
// folds this in when it lands.
func TestAttention108KindsMatchGeneratedTypeScript(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "types", "contracts.ts"))
	require.NoError(t, err)
	lines := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		lines[l] = true
	}
	kinds := AttentionKinds()
	require.Len(t, kinds, 13)
	for _, kind := range kinds {
		name := attentionKindConstName(kind)
		assert.True(t, lines["export const "+name+" = '"+kind+"' as const;"], "missing exact line for %s", kind)
		assert.True(t, lines["  | typeof "+name+";"] || lines["  | typeof "+name], "kind %s missing from the AttentionKind union", kind)
	}
	assert.True(t, lines["  type: 'server' | 'tool' | 'client' | 'setting';"], "AttentionSubject.type must list setting")
}
