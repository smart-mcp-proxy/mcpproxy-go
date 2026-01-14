package actor

import (
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// State represents the actor's connection state
type State string

const (
	// StateIdle means the actor is waiting for commands
	StateIdle State = "idle"

	// StateConnecting means the actor is attempting to connect
	StateConnecting State = "connecting"

	// StateConnected means the actor is connected and ready
	StateConnected State = "connected"

	// StateDisconnecting means the actor is disconnecting
	StateDisconnecting State = "disconnecting"

	// StateError means the actor encountered an error
	StateError State = "error"

	// StateStopped means the actor has been stopped
	StateStopped State = "stopped"
)

// Event represents an actor lifecycle event
type Event struct {
	Type      EventType
	State     State
	Timestamp time.Time
	Error     error
	Data      map[string]interface{}
}

// EventType describes the kind of actor event
type EventType string

const (
	// EventStateChanged means the actor changed state
	EventStateChanged EventType = "state_changed"

	// EventConnected means the actor successfully connected
	EventConnected EventType = "connected"

	// EventDisconnected means the actor disconnected
	EventDisconnected EventType = "disconnected"

	// EventError means the actor encountered an error
	EventError EventType = "error"

	// EventRetrying means the actor is retrying connection
	EventRetrying EventType = "retrying"
)

// Command represents a command sent to the actor
type Command struct {
	Type CommandType
	Data map[string]interface{}
}

// CommandType describes the kind of command
type CommandType string

const (
	// CommandConnect tells the actor to connect
	CommandConnect CommandType = "connect"

	// CommandDisconnect tells the actor to disconnect
	CommandDisconnect CommandType = "disconnect"

	// CommandStop tells the actor to stop
	CommandStop CommandType = "stop"

	// CommandUpdateConfig tells the actor to update its configuration
	CommandUpdateConfig CommandType = "update_config"
)

// ActorConfig holds the configuration for an actor
type ActorConfig struct {
	Name          string
	ServerConfig  *config.ServerConfig
	MaxRetries    int
	RetryInterval time.Duration
	ConnectTimeout time.Duration
}
