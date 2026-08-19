package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadConfiguredServers_EmitsServersChanged_OnExposePromptsFlip is the
// regression test for PR #973 review's P2 finding: flipping expose_prompts
// alone on a file hot-reload bumped the config version but hasChanged never
// counted it, so no servers.changed event fired and RefreshPrompts never
// ran — the toggle only took effect when coupled with a reconnect-forcing
// change or a restart.
func TestLoadConfiguredServers_EmitsServersChanged_OnExposePromptsFlip(t *testing.T) {
	rt, cfg := gateEnv(t, []map[string]any{
		{"name": "srv", "command": "./nonexistent", "protocol": "stdio", "enabled": false},
	}, nil)

	require.NoError(t, rt.LoadConfiguredServers(cfg))

	sub := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(sub)

	exposePrompts := false
	cfg.Servers[0].ExposePrompts = &exposePrompts
	require.NoError(t, rt.LoadConfiguredServers(cfg))

	// Other event types (e.g. activity.quarantine_change from the first load's
	// async supervisor work) may arrive first; drain until servers.changed shows up.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case evt := <-sub:
			if evt.Type == EventTypeServersChanged {
				return
			}
		case <-deadline:
			t.Fatal("expected a servers.changed event after flipping expose_prompts")
		}
	}
}

func TestBoolPtrEqual(t *testing.T) {
	tru := true
	fals := false
	tru2 := true

	assert.True(t, boolPtrEqual(nil, nil))
	assert.False(t, boolPtrEqual(nil, &tru))
	assert.False(t, boolPtrEqual(&tru, nil))
	assert.True(t, boolPtrEqual(&tru, &tru2))
	assert.False(t, boolPtrEqual(&tru, &fals))
}
