package managed

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestManagedClient_RefreshOAuthTokenDirect_NilClient verifies that
// RefreshOAuthTokenDirect returns an error when the managed client is nil.
func TestManagedClient_RefreshOAuthTokenDirect_NilClient(t *testing.T) {
	var mc *Client
	err := mc.RefreshOAuthTokenDirect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not initialized")
}

// TestManagedClient_RefreshOAuthTokenDirect_NilCoreClient verifies that
// RefreshOAuthTokenDirect returns an error when the core client is nil
// (managed client exists but never initialized its core).
func TestManagedClient_RefreshOAuthTokenDirect_NilCoreClient(t *testing.T) {
	mc := &Client{
		coreClient: nil,
	}
	err := mc.RefreshOAuthTokenDirect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not initialized")
}
