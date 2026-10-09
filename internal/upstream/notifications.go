package upstream

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// NotificationLevel represents the level of a notification
type NotificationLevel int

const (
	NotificationInfo NotificationLevel = iota
	NotificationWarning
	NotificationError
)

// String constants for notification levels
const (
	errorLevelString   = "Error"
	unknownLevelString = "Unknown"
)

// String returns the string representation of the notification level
func (l NotificationLevel) String() string {
	switch l {
	case NotificationInfo:
		return "Info"
	case NotificationWarning:
		return "Warning"
	case NotificationError:
		return errorLevelString
	default:
		return unknownLevelString
	}
}

// Notification represents a notification to be sent to the UI
type Notification struct {
	Level      NotificationLevel `json:"level"`
	Title      string            `json:"title"`
	Message    string            `json:"message"`
	ServerName string            `json:"server_name,omitempty"`
	Timestamp  time.Time         `json:"timestamp"`
}

// NotificationHandler defines the interface for handling notifications
type NotificationHandler interface {
	SendNotification(notification *Notification)
}

// NotificationManager manages notification handlers and provides convenience methods
type NotificationManager struct {
	handlers []NotificationHandler
}

// NewNotificationManager creates a new notification manager
func NewNotificationManager() *NotificationManager {
	return &NotificationManager{
		handlers: make([]NotificationHandler, 0),
	}
}

// AddHandler adds a notification handler
func (nm *NotificationManager) AddHandler(handler NotificationHandler) {
	nm.handlers = append(nm.handlers, handler)
}

// SendNotification sends a notification to all registered handlers
func (nm *NotificationManager) SendNotification(notification *Notification) {
	// Set timestamp if not provided
	if notification.Timestamp.IsZero() {
		notification.Timestamp = time.Now()
	}

	// Send to all handlers
	for _, handler := range nm.handlers {
		// Use goroutine to avoid blocking
		go handler.SendNotification(notification)
	}
}

// Helper methods for common notifications

// NotifyServerConnected sends a notification when a server connects
func (nm *NotificationManager) NotifyServerConnected(serverName string) {
	nm.SendNotification(&Notification{
		Level:      NotificationInfo,
		Title:      "Server Connected",
		Message:    fmt.Sprintf("Successfully connected to %s", serverName),
		ServerName: serverName,
	})
}

// NotifyServerDisconnected sends a notification when a server disconnects
func (nm *NotificationManager) NotifyServerDisconnected(serverName string, reason error) {
	level := NotificationWarning
	message := fmt.Sprintf("Disconnected from %s", serverName)
	if reason != nil {
		message = fmt.Sprintf("Disconnected from %s: %s", serverName, reason.Error())
		level = NotificationError
	}

	nm.SendNotification(&Notification{
		Level:      level,
		Title:      "Server Disconnected",
		Message:    message,
		ServerName: serverName,
	})
}

// NotifyServerError sends a notification when a server encounters an error
func (nm *NotificationManager) NotifyServerError(serverName string, err error) {
	nm.SendNotification(&Notification{
		Level:      NotificationError,
		Title:      "Server Error",
		Message:    fmt.Sprintf("Error with %s: %s", serverName, err.Error()),
		ServerName: serverName,
	})
}

// NotifyOAuthRequired sends a notification when OAuth authentication is required
func (nm *NotificationManager) NotifyOAuthRequired(serverName string) {
	nm.SendNotification(&Notification{
		Level:      NotificationInfo,
		Title:      "Authentication Required",
		Message:    fmt.Sprintf("OAuth authentication required for %s", serverName),
		ServerName: serverName,
	})
}

// NotifyServerConnecting sends a notification when a server starts connecting
func (nm *NotificationManager) NotifyServerConnecting(serverName string) {
	nm.SendNotification(&Notification{
		Level:      NotificationInfo,
		Title:      "Server Connecting",
		Message:    fmt.Sprintf("Connecting to %s...", serverName),
		ServerName: serverName,
	})
}

// StateChangeNotifier creates state change notifications based on state transitions
func StateChangeNotifier(nm *NotificationManager, serverName string) func(oldState, newState types.ConnectionState, info *types.ConnectionInfo) {
	// probePromptShown is set once a speculative Pending Auth park (GH #1537)
	// has prompted for sign-in. Such a park is re-probed every
	// GaveUpProbeInterval (PendingAuth → Connecting → PendingAuth), and each
	// re-park must not prompt again. Only Ready ends the episode: Ready and
	// PendingAuth callbacks are dispatched synchronously, in transition order,
	// whereas SetError's callback runs on its own goroutine and could land
	// after a newer park — so an Error must never touch this flag.
	var probePromptShown atomic.Bool
	return func(oldState, newState types.ConnectionState, info *types.ConnectionInfo) {
		// Only send notifications for significant state changes
		switch newState {
		case types.StateConnecting:
			// Notify when connection attempt starts (important for UI feedback after OAuth)
			if oldState != types.StateConnecting {
				nm.NotifyServerConnecting(serverName)
			}
		case types.StateReady:
			probePromptShown.Store(false)
			if oldState != types.StateReady {
				nm.NotifyServerConnected(serverName)
			}
		case types.StateError:
			if info.LastError != nil {
				nm.NotifyServerError(serverName, info.LastError)
			}
		case types.StateAuthenticating:
			if oldState == types.StateConnecting {
				nm.NotifyOAuthRequired(serverName)
			}
		case types.StatePendingAuth:
			// Parked awaiting user login: the connection attempt is over and only a
			// human can move it forward, so surface the login prompt once.
			if oldState == types.StatePendingAuth {
				break
			}
			probe := info != nil && info.PendingAuthProbe
			if probe && probePromptShown.Load() {
				break // a periodic re-probe of the same unconfirmed park
			}
			probePromptShown.Store(probe)
			nm.NotifyOAuthRequired(serverName)
		case types.StateDisconnected:
			if oldState == types.StateReady {
				nm.NotifyServerDisconnected(serverName, info.LastError)
			}
		}
	}
}
