package storage

import (
	"context"
	"fmt"
)

// Manager wrappers for the Spec 123 PR-3 operations in oauth_tools.go. They
// live in their own file because the source test of the sweep shape
// (oauth_sweep_source_test.go) indexes functions by name in oauth_tools.go.

// SealOAuthTokens is BoltDB.SealOAuthTokens through the manager lock.
func (m *Manager) SealOAuthTokens(ctx context.Context, dryRun bool) (OAuthSealReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return OAuthSealReport{}, fmt.Errorf("storage not initialized")
	}
	return m.db.SealOAuthTokens(ctx, dryRun)
}

// DecryptOAuthTokens is BoltDB.DecryptOAuthTokens through the manager lock.
func (m *Manager) DecryptOAuthTokens(ctx context.Context) (OAuthDecryptReport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.db == nil {
		return OAuthDecryptReport{}, fmt.Errorf("storage not initialized")
	}
	return m.db.DecryptOAuthTokens(ctx)
}

// OAuthCompactAdvised is BoltDB.OAuthCompactAdvised through the manager lock.
func (m *Manager) OAuthCompactAdvised() (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.db == nil {
		return false, fmt.Errorf("storage not initialized")
	}
	return m.db.OAuthCompactAdvised()
}
