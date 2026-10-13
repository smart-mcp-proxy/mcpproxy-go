package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDetectionTextIsInternalOnly(t *testing.T) {
	rt := &Runtime{
		eventSubs:         make(map[chan Event]struct{}),
		internalEventSubs: make(map[chan Event]struct{}),
	}
	external := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(external)
	internal := rt.subscribeInternalEvents()
	defer rt.UnsubscribeEvents(internal)

	rt.EmitActivityToolCallCompleted(
		"github", "echo", "session", "request", "internal", "success", "", 1,
		nil, "display-limited", true, "", nil, "", "", 0, 0,
		"detector-only-secret", nil, "parent",
	)

	select {
	case event := <-external:
		require.NotContains(t, event.Payload, "detection_text")
	case <-time.After(time.Second):
		t.Fatal("external subscriber did not receive event")
	}
	select {
	case event := <-internal:
		require.Equal(t, "detector-only-secret", event.Payload["detection_text"])
	case <-time.After(time.Second):
		t.Fatal("internal subscriber did not receive event")
	}
}
