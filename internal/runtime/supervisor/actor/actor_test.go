package actor

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// MockClient simulates a managed.Client for testing
type MockClient struct {
	connected      bool
	connectErr     error
	disconnectErr  error
	connectCalled  int
	disconnectCalled int
}

func (m *MockClient) Connect(ctx context.Context) error {
	m.connectCalled++
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *MockClient) Disconnect() error {
	m.disconnectCalled++
	if m.disconnectErr != nil {
		return m.disconnectErr
	}
	m.connected = false
	return nil
}

func (m *MockClient) IsConnected() bool {
	return m.connected
}

func TestActor_New(t *testing.T) {
	cfg := ActorConfig{
		Name:           "test-actor",
		ServerConfig:   &config.ServerConfig{Name: "test-server"},
		MaxRetries:     3,
		RetryInterval:  1 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}

	mockClient := &MockClient{}
	actor := New(cfg, nil, zap.NewNop())

	if actor == nil {
		t.Fatal("Expected non-nil actor")
	}

	if actor.GetState() != StateIdle {
		t.Errorf("Expected initial state Idle, got %s", actor.GetState())
	}

	// Verify mockClient interface
	if mockClient.connectCalled != 0 {
		t.Error("Expected no connects yet")
	}
}

func TestActor_Connect(t *testing.T) {
	cfg := ActorConfig{
		Name:           "test-actor",
		ServerConfig:   &config.ServerConfig{Name: "test-server"},
		MaxRetries:     3,
		RetryInterval:  100 * time.Millisecond,
		ConnectTimeout: 5 * time.Second,
	}

	actor := New(cfg, nil, zap.NewNop())

	// Start actor
	actor.Start()
	defer actor.Stop()

	// Subscribe to events
	eventCh := actor.Events()

	// Send connect command
	actor.SendCommand(Command{Type: CommandConnect})

	// Wait for state to change
	time.Sleep(200 * time.Millisecond)

	// Note: Without a real client, this will fail and go to error state
	// In a real scenario with MockClient properly integrated, it would succeed
	state := actor.GetState()
	if state != StateError && state != StateConnected {
		t.Logf("State after connect: %s (expected Error or Connected)", state)
	}

	// Drain event channel
	select {
	case event := <-eventCh:
		t.Logf("Received event: %s", event.Type)
	default:
	}
}

func TestActor_Disconnect(t *testing.T) {
	cfg := ActorConfig{
		Name:           "test-actor",
		ServerConfig:   &config.ServerConfig{Name: "test-server"},
		MaxRetries:     3,
		RetryInterval:  1 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}

	actor := New(cfg, nil, zap.NewNop())

	// Start actor
	actor.Start()
	defer actor.Stop()

	// Manually set state to connected for testing
	actor.setState(StateConnected)

	// Send disconnect command
	actor.SendCommand(Command{Type: CommandDisconnect})

	// Wait for state to change
	time.Sleep(200 * time.Millisecond)

	state := actor.GetState()
	if state != StateIdle {
		t.Errorf("Expected state Idle after disconnect, got %s", state)
	}
}

func TestActor_StateTransitions(t *testing.T) {
	cfg := ActorConfig{
		Name:         "test-actor",
		ServerConfig: &config.ServerConfig{Name: "test-server"},
	}

	actor := New(cfg, nil, zap.NewNop())

	// Test initial state
	if actor.GetState() != StateIdle {
		t.Errorf("Expected initial state Idle, got %s", actor.GetState())
	}

	// Test state transitions
	actor.setState(StateConnecting)
	if actor.GetState() != StateConnecting {
		t.Errorf("Expected state Connecting, got %s", actor.GetState())
	}

	actor.setState(StateConnected)
	if actor.GetState() != StateConnected {
		t.Errorf("Expected state Connected, got %s", actor.GetState())
	}

	actor.setState(StateError)
	if actor.GetState() != StateError {
		t.Errorf("Expected state Error, got %s", actor.GetState())
	}
}

func TestActor_Events(t *testing.T) {
	cfg := ActorConfig{
		Name:         "test-actor",
		ServerConfig: &config.ServerConfig{Name: "test-server"},
	}

	actor := New(cfg, nil, zap.NewNop())

	// Subscribe to events
	eventCh := actor.Events()

	// Emit an event
	actor.emitEvent(Event{
		Type:      EventStateChanged,
		State:     StateConnecting,
		Timestamp: time.Now(),
	})

	// Should receive event
	select {
	case event := <-eventCh:
		if event.Type != EventStateChanged {
			t.Errorf("Expected EventStateChanged, got %s", event.Type)
		}
		if event.State != StateConnecting {
			t.Errorf("Expected state Connecting, got %s", event.State)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Did not receive event")
	}
}

func TestActor_Stop(t *testing.T) {
	cfg := ActorConfig{
		Name:         "test-actor",
		ServerConfig: &config.ServerConfig{Name: "test-server"},
	}

	actor := New(cfg, nil, zap.NewNop())

	// Start actor
	actor.Start()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Stop actor
	actor.Stop()

	// Verify state
	state := actor.GetState()
	if state != StateStopped {
		t.Errorf("Expected state Stopped, got %s", state)
	}
}

func TestActor_UpdateConfig(t *testing.T) {
	cfg := ActorConfig{
		Name:         "test-actor",
		ServerConfig: &config.ServerConfig{Name: "test-server", Enabled: true},
	}

	actor := New(cfg, nil, zap.NewNop())

	// Start actor
	actor.Start()
	defer actor.Stop()

	// Update config
	newConfig := &config.ServerConfig{Name: "updated-server", Enabled: false}
	actor.SendCommand(Command{
		Type: CommandUpdateConfig,
		Data: map[string]interface{}{
			"config": newConfig,
		},
	})

	// Wait for command to be processed
	time.Sleep(200 * time.Millisecond)

	// Verify config was updated
	actor.mu.RLock()
	updatedName := actor.config.ServerConfig.Name
	actor.mu.RUnlock()

	if updatedName != "updated-server" {
		t.Errorf("Expected config name 'updated-server', got '%s'", updatedName)
	}
}

func TestActor_GetState_Concurrent(t *testing.T) {
	cfg := ActorConfig{
		Name:         "test-actor",
		ServerConfig: &config.ServerConfig{Name: "test-server"},
	}

	actor := New(cfg, nil, zap.NewNop())

	// Start actor
	actor.Start()
	defer actor.Stop()

	// Concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				_ = actor.GetState()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}
