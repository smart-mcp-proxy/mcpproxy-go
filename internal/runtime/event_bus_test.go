package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestEventBus_ConfigSavedEvent(t *testing.T) {
	// Create logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	// Create minimal runtime for testing event bus
	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	// Create a done channel to signal when we receive the event
	done := make(chan Event, 1)

	// Listen for config.saved event in background
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeConfigSaved {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for config.saved event")
		}
	}()

	// Trigger emitConfigSaved
	testPath := "/test/config.json"
	rt.emitConfigSaved(testPath)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeConfigSaved, evt.Type, "Event type should be config.saved")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, testPath, evt.Payload["path"], "Event payload should contain config path")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive config.saved event within timeout")
	}
}

func TestEventBus_ConfigReloadedEvent(t *testing.T) {
	// Create logger
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for config.reloaded event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeConfigReloaded {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for config.reloaded event")
		}
	}()

	// Trigger emitConfigReloaded
	testPath := "/test/config.json"
	rt.emitConfigReloaded(testPath)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeConfigReloaded, evt.Type, "Event type should be config.reloaded")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, testPath, evt.Payload["path"], "Event payload should contain config path")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive config.reloaded event within timeout")
	}
}

func TestEventBus_ServersChangedEvent(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Subscribe to events
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	done := make(chan Event, 1)

	// Listen for servers.changed event
	go func() {
		select {
		case evt := <-eventChan:
			if evt.Type == EventTypeServersChanged {
				done <- evt
			}
		case <-time.After(2 * time.Second):
			t.Log("Timeout waiting for servers.changed event")
		}
	}()

	// Trigger emitServersChanged with custom payload
	extra := map[string]any{
		"server":  "test-server",
		"enabled": true,
	}
	rt.emitServersChanged("test_reason", extra)

	// Wait for event
	select {
	case evt := <-done:
		assert.Equal(t, EventTypeServersChanged, evt.Type, "Event type should be servers.changed")
		assert.NotNil(t, evt.Payload, "Event payload should not be nil")
		assert.Equal(t, "test_reason", evt.Payload["reason"], "Event should contain reason")
		assert.Equal(t, "test-server", evt.Payload["server"], "Event should contain server name")
		assert.Equal(t, true, evt.Payload["enabled"], "Event should contain enabled flag")
		assert.NotZero(t, evt.Timestamp, "Event should have a timestamp")
	case <-time.After(2 * time.Second):
		t.Fatal("Did not receive servers.changed event within timeout")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Create multiple subscribers
	sub1 := rt.SubscribeEvents()
	sub2 := rt.SubscribeEvents()
	sub3 := rt.SubscribeEvents()

	defer rt.UnsubscribeEvents(sub1)
	defer rt.UnsubscribeEvents(sub2)
	defer rt.UnsubscribeEvents(sub3)

	// Create done channels for each subscriber
	done1 := make(chan Event, 1)
	done2 := make(chan Event, 1)
	done3 := make(chan Event, 1)

	// Listen on all subscribers
	go func() {
		select {
		case evt := <-sub1:
			done1 <- evt
		case <-time.After(2 * time.Second):
			t.Log("Subscriber 1 timeout")
		}
	}()

	go func() {
		select {
		case evt := <-sub2:
			done2 <- evt
		case <-time.After(2 * time.Second):
			t.Log("Subscriber 2 timeout")
		}
	}()

	go func() {
		select {
		case evt := <-sub3:
			done3 <- evt
		case <-time.After(2 * time.Second):
			t.Log("Subscriber 3 timeout")
		}
	}()

	// Emit single event
	rt.emitConfigSaved("/test/path")

	// All subscribers should receive the event
	timeout := time.After(2 * time.Second)
	receivedCount := 0

	for i := 0; i < 3; i++ {
		select {
		case evt := <-done1:
			assert.Equal(t, EventTypeConfigSaved, evt.Type)
			receivedCount++
		case evt := <-done2:
			assert.Equal(t, EventTypeConfigSaved, evt.Type)
			receivedCount++
		case evt := <-done3:
			assert.Equal(t, EventTypeConfigSaved, evt.Type)
			receivedCount++
		case <-timeout:
			t.Fatalf("Only %d of 3 subscribers received the event", receivedCount)
		}
	}

	assert.Equal(t, 3, receivedCount, "All 3 subscribers should have received the event")
}

func TestEventBus_ConcurrentPublish(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)
	defer logger.Sync()

	rt := &Runtime{
		logger:    logger,
		eventSubs: make(map[chan Event]struct{}),
	}

	// Create subscriber
	eventChan := rt.SubscribeEvents()
	defer rt.UnsubscribeEvents(eventChan)

	// Count received events
	receivedCount := 0
	done := make(chan struct{})

	go func() {
		for evt := range eventChan {
			if evt.Type == EventTypeConfigSaved {
				receivedCount++
				if receivedCount == 10 {
					close(done)
					return
				}
			}
		}
	}()

	// Emit 10 events concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			time.Sleep(time.Millisecond * time.Duration(idx*10))
			rt.emitConfigSaved("/test/path")
		}(i)
	}

	wg.Wait()

	// Wait for all events to be received
	select {
	case <-done:
		assert.Equal(t, 10, receivedCount, "Should receive all 10 events")
	case <-time.After(3 * time.Second):
		t.Fatalf("Only received %d of 10 events", receivedCount)
	}
}

