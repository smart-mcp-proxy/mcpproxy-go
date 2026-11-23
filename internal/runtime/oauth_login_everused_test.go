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

// The epoch counts a switch-off only when the commit reports a successful
// publish; the lifecycle lock is released either way.
func TestDisableEpoch_CountsOnlyPublishedSwitchOffs(t *testing.T) {
	fx := newOAuthFx(t)
	svc := NewOAuthLoginService(OAuthLoginDeps{Store: fx.sm})
	assert.Zero(t, svc.DisableEpoch())
	release := svc.beginDisable("test")
	assert.Zero(t, svc.DisableEpoch(), "not counted before the disabled config is published")
	svc.disablePublished()
	release()
	assert.Equal(t, uint64(1), svc.DisableEpoch())
}
