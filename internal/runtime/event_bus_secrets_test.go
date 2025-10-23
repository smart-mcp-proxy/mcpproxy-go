package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"mcpproxy-go/internal/config"
)

func TestEmitSecretsChanged(t *testing.T) {
	logger := zap.NewNop()

	// Create minimal runtime for testing event bus
	rt := &Runtime{
		logger:    logger,
		eventMu:   sync.RWMutex{},
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventsCh := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventsCh)

	// Channel to receive event in goroutine
	done := make(chan Event, 1)

	// Start listening for events
	go func() {
		select {
		case evt := <-eventsCh:
			done <- evt
		case <-time.After(2 * time.Second):
			// Timeout
		}
	}()

	// Trigger emitSecretsChanged
	testSecretName := "test_secret"
	testOperation := "store"
	extra := map[string]any{
		"test_field": "test_value",
	}
	rt.emitSecretsChanged(testOperation, testSecretName, extra)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeSecretsChanged, evt.Type, "Event type should be secrets.changed")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, testOperation, evt.Payload["operation"], "Event should contain operation")
		assert.Equal(t, testSecretName, evt.Payload["secret_name"], "Event should contain secret name")
		assert.Equal(t, "test_value", evt.Payload["test_field"], "Event should contain extra fields")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive secrets.changed event within timeout")
	}
}

func TestNotifySecretsChanged_NoAffectedServers(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Servers: []*config.ServerConfig{
			{
				Name:     "test-server",
				Protocol: "stdio",
				Command:  "echo",
				Args:     []string{"test"},
				Enabled:  true,
			},
		},
	}

	rt, err := New(cfg, "", logger)
	assert.NoError(t, err)
	assert.NotNil(t, rt)
	defer rt.Close()

	// Subscribe to events
	eventsCh := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventsCh)

	// Notify about a secret that isn't used by any server
	err = rt.NotifySecretsChanged(rt.AppContext(), "store", "unused_secret")
	assert.NoError(t, err)

	// Should still emit secrets.changed event
	select {
	case evt := <-eventsCh:
		assert.Equal(t, EventTypeSecretsChanged, evt.Type)
		assert.Equal(t, "store", evt.Payload["operation"])
		assert.Equal(t, "unused_secret", evt.Payload["secret_name"])
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive secrets.changed event")
	}
}

func TestNotifySecretsChanged_WithAffectedServers(t *testing.T) {
	logger := zap.NewNop()
	cfg := &config.Config{
		DataDir: t.TempDir(),
		Servers: []*config.ServerConfig{
			{
				Name:     "server-with-secret",
				Protocol: "stdio",
				Command:  "echo",
				Args:     []string{"test"},
				Env: map[string]string{
					"API_KEY": "${keyring:my_secret}",
				},
				Enabled: true,
			},
			{
				Name:     "server-without-secret",
				Protocol: "stdio",
				Command:  "echo",
				Args:     []string{"test"},
				Enabled:  true,
			},
		},
	}

	rt, err := New(cfg, "", logger)
	assert.NoError(t, err)
	assert.NotNil(t, rt)
	defer rt.Close()

	// Subscribe to events
	eventsCh := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventsCh)

	// Collect events with proper synchronization
	var eventsMu sync.Mutex
	events := make([]Event, 0)
	stopCollecting := make(chan struct{})
	collectorDone := make(chan struct{})

	go func() {
		defer close(collectorDone)
		for {
			select {
			case evt := <-eventsCh:
				eventsMu.Lock()
				events = append(events, evt)
				eventsMu.Unlock()
			case <-stopCollecting:
				return
			}
		}
	}()

	// Notify about the secret that IS used
	err = rt.NotifySecretsChanged(rt.AppContext(), "store", "my_secret")
	assert.NoError(t, err)

	// Wait for events to be processed
	time.Sleep(1 * time.Second)

	// Stop collecting events and wait for collector to finish
	close(stopCollecting)
	<-collectorDone

	// Check that we received secrets.changed event
	eventsMu.Lock()
	foundSecretsChanged := false
	for _, evt := range events {
		if evt.Type == EventTypeSecretsChanged {
			foundSecretsChanged = true
			assert.Equal(t, "store", evt.Payload["operation"])
			assert.Equal(t, "my_secret", evt.Payload["secret_name"])
		}
	}
	eventsMu.Unlock()

	assert.True(t, foundSecretsChanged, "Should have received secrets.changed event")

	// Note: Server restart events (servers.changed) would also be emitted,
	// but testing the full restart flow requires a more complex E2E test
}
