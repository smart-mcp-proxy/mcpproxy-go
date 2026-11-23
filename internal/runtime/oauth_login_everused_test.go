package runtime

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// brokenMarkerStore cannot read the ever-used marker.
type brokenMarkerStore struct{ OAuthLoginStore }

func (brokenMarkerStore) OAuthLoginEverUsed() (bool, error) { return false, errors.New("disk on fire") }

// An unreadable marker is read as "used" (fail closed), and the answer is not
// cached: a later successful read wins.
func TestEverUsed_ReadErrorFailsClosed(t *testing.T) {
	fx := newOAuthFx(t)
	svc := NewOAuthLoginService(OAuthLoginDeps{Store: brokenMarkerStore{fx.sm}})
	assert.True(t, svc.EverUsed(), "a read error must not look like a never-used install")

	healthy := NewOAuthLoginService(OAuthLoginDeps{Store: fx.sm})
	assert.False(t, healthy.EverUsed(), "a successful read of an unused install is false")
}

// Every switch-off boundary bumps the epoch, so a request authenticated before
// an off -> on can be told apart from one authenticated after it.
func TestDisableEpoch_CountsSwitchOffs(t *testing.T) {
	fx := newOAuthFx(t)
	svc := NewOAuthLoginService(OAuthLoginDeps{Store: fx.sm})
	assert.Zero(t, svc.DisableEpoch())
	svc.beginDisable("test")()
	svc.beginDisable("test")()
	assert.Equal(t, uint64(2), svc.DisableEpoch())
}
