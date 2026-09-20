package logs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestReadUpstreamServerLogTail_NilConfigDoesNotPanic and its attributed
// sibling below: a *config.LogConfig of nil (a minimal test/library caller
// that constructs config.Config{} directly rather than through
// config.DefaultConfig(), which always sets Logging) must not crash the
// reader — GetLogFilePathWithDir already falls back to the OS default log
// directory for an EMPTY LogDir string (paths.go:109-112); the bug was
// dereferencing config.LogDir on a nil config before ever reaching that
// fallback. Found by internal/server's scope_http_matrix_test.go (Spec 105
// PR H1, T111), which drives handleTailLog over real HTTP against
// profileTestEnv's config (built as a struct literal — e2e_test.go:34 —
// leaving Logging nil): the panic surfaced as mcp-go's recovered
// "-32603 panic recovered in upstream_servers tool handler", not a full
// server crash, but it is still a genuine defect — a legitimate tail_log
// call for EITHER caller kind (this affects the whole-file admin reader
// identically, so it is a robustness gap, not a scope/security issue) fails
// with a confusing internal error instead of the sensible empty/OS-default
// behavior every other empty-LogDir caller gets.
func TestReadUpstreamServerLogTail_NilConfigDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := ReadUpstreamServerLogTail(nil, "some-server", 10)
		// No log file exists at the OS-default location for this made-up
		// server name, so an empty result (not an error) is expected —
		// mirroring the existing empty-LogDir/missing-file behavior.
		assert.NoError(t, err)
	})
}

func TestReadUpstreamServerLogTailAttributed_NilConfigDoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		_, err := ReadUpstreamServerLogTailAttributed(nil, "some-server", 10)
		assert.NoError(t, err)
	})
}
