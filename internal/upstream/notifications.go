package upstream

import (
	"fmt"
	"time"
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

// StateChangeNotifier creates state change notifications based on state transitions
func StateChangeNotifier(nm *NotificationManager, serverName string) func(oldState, newState ConnectionState, info ConnectionInfo) {
	return func(oldState, newState ConnectionState, info ConnectionInfo) {
		// Only send notifications for significant state changes
		switch newState {
		case StateReady:
			if oldState != StateReady {
				nm.NotifyServerConnected(serverName)
			}
		case StateError:
			if info.LastError != nil {
				nm.NotifyServerError(serverName, info.LastError)
			}
		case StateAuthenticating:
			if oldState == StateConnecting {
				nm.NotifyOAuthRequired(serverName)
			}
		case StateDisconnected:
			if oldState == StateReady {
				nm.NotifyServerDisconnected(serverName, info.LastError)
			}
		}
	}
}
