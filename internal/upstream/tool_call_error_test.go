package upstream

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGenericToolCallError(t *testing.T) {
	cause := errors.New("validation failed for argument 'synthetic'")
	wrapped := &genericToolCallError{server: "fixture", tool: "check", cause: cause}
	previous := fmt.Errorf("tool '%s' on server '%s' failed: %w. Check server configuration, authentication, and tool parameters", "check", "fixture", cause)
	require.Equal(t, previous.Error(), wrapped.Error(), "byte-identical existing manager diagnostics")
	require.ErrorIs(t, wrapped, cause, "preserve the existing unwrap/identity contract")
	got, trusted := GenericToolCallCause(wrapped)
	require.True(t, trusted)
	require.Same(t, cause, got)
	for _, other := range []error{nil, cause, previous, fmt.Errorf("authentication failed: %w", wrapped), fmt.Errorf("outer: %w", wrapped)} {
		got, trusted = GenericToolCallCause(other)
		require.False(t, trusted, "only the exact top-level manager wrapper grants cause selection")
		require.Nil(t, got)
	}
}
