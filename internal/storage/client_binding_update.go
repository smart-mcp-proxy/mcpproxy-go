package storage

import (
	"fmt"

	"go.etcd.io/bbolt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// UpdateOAuthClientBinding rewrites the binding of a client that has a
// browser-login entry, whether or not its API-key record is active (an
// UpdateClientCredentialBinding of a revoked or absent API key refuses). It
// goes through the one binding writer, so an API-key record that exists is kept
// equal. It returns the binding the client held before (the API-key record
// wins, as everywhere on the request path). ErrClientCredentialNotFound means
// the client has no browser-login entry.
func (m *Manager) UpdateOAuthClientBinding(clientID, pin, mode string) (beforePin, beforeMode string, err error) {
	if !auth.ValidClientID(clientID) {
		return "", "", fmt.Errorf("invalid client id %q", clientID)
	}
	if err := validateClientBinding(pin, mode); err != nil {
		return "", "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	err = m.db.db.Update(func(tx *bbolt.Tx) error {
		b, err := getOAuthBindingTx(tx, clientID)
		if err != nil {
			return err
		}
		if b == nil {
			return ErrClientCredentialNotFound
		}
		var exists bool
		beforePin, beforeMode, exists, err = currentClientBindingTx(tx, clientID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrClientCredentialNotFound
		}
		return writeClientBindingOptsTx(tx, clientID, pin, mode, true)
	})
	return beforePin, beforeMode, err
}
