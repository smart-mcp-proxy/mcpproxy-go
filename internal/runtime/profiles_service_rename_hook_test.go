package runtime

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingSessionHook records the order of the session hook calls.
type recordingSessionHook struct {
	rt     *Runtime
	events []string
}

func (h *recordingSessionHook) HoldProfileNotifications() func() {
	published := false
	for _, p := range h.rt.Config().Profiles {
		published = published || p.Name == "ro2"
	}
	h.events = append(h.events, fmt.Sprintf("hold(renamed-published=%v)", published))
	return func() { h.events = append(h.events, "release") }
}

func (h *recordingSessionHook) ProfileRenamed(from, to string) {
	h.events = append(h.events, "renamed:"+from+"->"+to)
}
func (h *recordingSessionHook) ProfileDeleted(name string)        {}
func (h *recordingSessionHook) ProfileTokensMoved(names []string) {}

// F5.2: a rename holds the profile-change notifications BEFORE the new snapshot
// is published and releases them only after the session selections were
// rewritten, so a client's re-list can never resolve a stale selection.
func TestProfilesService_RenameHoldsNotificationsAcrossTheSessionRewrite(t *testing.T) {
	h := newProfilesHarness(t)
	hook := &recordingSessionHook{rt: h.rt}
	h.svc.SetSessionHook(hook)

	_, err := h.svc.Rename(context.Background(), h.actor(), "ro", "ro2")
	require.NoError(t, err)
	assert.Equal(t, []string{"hold(renamed-published=false)", "renamed:ro->ro2", "release"}, hook.events)
}
