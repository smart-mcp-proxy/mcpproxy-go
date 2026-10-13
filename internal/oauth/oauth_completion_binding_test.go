package oauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/commitview"
)

// Spec 116 PR2b (#1607) review r8: the in-memory recent-completion flag is
// bound like the DB event. A login completion of an instance a commit
// superseded (refused by the binding-checked DB write) records nothing, and a
// completion recorded for one binding never reads as recent for another, so a
// same-name replacement keeps its automatic OAuth deferral.

type fakeBoundNotifier struct{ written bool }

func (f fakeBoundNotifier) SaveOAuthCompletionEventIfBound(*CompletionEvent, commitview.CredentialBinding) (bool, error) {
	return f.written, nil
}

func forgetCompletion(name string) {
	m := GetTokenStoreManager()
	m.mu.Lock()
	delete(m.completedOAuth, name)
	m.mu.Unlock()
}

func TestBoundCompletionRefusedRecordsNothing(t *testing.T) {
	const name = "r8-refused-completion"
	forgetCompletion(name)
	t.Cleanup(func() { forgetCompletion(name) })
	old := commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 1}
	repl := commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 3}
	m := GetTokenStoreManager()

	require.NoError(t, m.MarkOAuthCompletedWithDBBound(name, fakeBoundNotifier{written: false}, old))

	assert.False(t, m.HasRecentOAuthCompletion(name), "refused completion must not set the name-only flag")
	assert.False(t, m.HasRecentOAuthCompletionBound(name, repl), "refused completion must not read as recent for the replacement")
	assert.False(t, m.HasRecentOAuthCompletionBound(name, old), "refused completion must not read as recent at all")
}

func TestBoundCompletionIsRecentOnlyForItsBinding(t *testing.T) {
	const name = "r8-bound-completion"
	forgetCompletion(name)
	t.Cleanup(func() { forgetCompletion(name) })
	old := commitview.CredentialBinding{Incarnation: 1, OAuthEpoch: 1}
	repl := commitview.CredentialBinding{Incarnation: 3, OAuthEpoch: 3}
	m := GetTokenStoreManager()

	require.NoError(t, m.MarkOAuthCompletedWithDBBound(name, fakeBoundNotifier{written: true}, old))
	assert.True(t, m.HasRecentOAuthCompletionBound(name, old))
	assert.False(t, m.HasRecentOAuthCompletionBound(name, repl), "an old incarnation's completion is not the replacement's")
	assert.True(t, m.HasRecentOAuthCompletionBound(name, commitview.CredentialBinding{}), "an unbound reader keeps name-only semantics")

	forgetCompletion(name)
	m.MarkOAuthCompletedBound(name, old)
	assert.True(t, m.HasRecentOAuthCompletionBound(name, old))
	assert.False(t, m.HasRecentOAuthCompletionBound(name, repl))

	// A legacy unbound mark (CLI process) keeps matching every reader.
	forgetCompletion(name)
	m.MarkOAuthCompleted(name)
	assert.True(t, m.HasRecentOAuthCompletionBound(name, repl))
}
