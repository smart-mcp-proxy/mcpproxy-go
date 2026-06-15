//go:build server

package multiuser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 074 T7 / FR-018: a shared upstream brokered per-user must key its
// connection by (user, server) so one user's credential/connection is never
// reused for another. The router exposes the per-(user,server) connection key
// for the connection pool to use.

func TestRouter_BrokeredConnectionKey_DistinctPerUser(t *testing.T) {
	router, _ := setupRouter(t, []string{"shared-ghe"}, nil)

	aliceKey, err := router.BrokeredConnectionKey(userCtx("alice"), "shared-ghe")
	require.NoError(t, err)
	bobKey, err := router.BrokeredConnectionKey(userCtx("bob"), "shared-ghe")
	require.NoError(t, err)

	assert.NotEmpty(t, aliceKey)
	assert.NotEqual(t, aliceKey, bobKey,
		"the same shared brokered upstream must key a distinct connection per user (FR-018)")

	// Stable for the same (user, server).
	aliceKey2, err := router.BrokeredConnectionKey(userCtx("alice"), "shared-ghe")
	require.NoError(t, err)
	assert.Equal(t, aliceKey, aliceKey2, "connection key must be stable for the same (user, server)")
}

func TestRouter_BrokeredConnectionKey_RequiresAuth(t *testing.T) {
	router, _ := setupRouter(t, []string{"shared-ghe"}, nil)
	_, err := router.BrokeredConnectionKey(noAuthCtx(), "shared-ghe")
	assert.Error(t, err, "no auth context must be rejected")
}

func TestRouter_BrokeredConnectionKey_UnknownServer(t *testing.T) {
	router, _ := setupRouter(t, []string{"shared-ghe"}, nil)
	_, err := router.BrokeredConnectionKey(userCtx("alice"), "nope")
	assert.Error(t, err, "unknown/inaccessible server must error")
}
