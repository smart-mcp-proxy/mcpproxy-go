//go:build !nogui && !headless && !linux

package tray

import (
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

	"github.com/gen2brain/beeep"
	"go.uber.org/zap"
)

// NotificationHandler implements upstream.NotificationHandler for system tray notifications
type NotificationHandler struct {
	logger *zap.SugaredLogger
}

// NewNotificationHandler creates a new tray notification handler
func NewNotificationHandler(logger *zap.SugaredLogger) *NotificationHandler {
	return &NotificationHandler{
		logger: logger,
	}
}

// SendNotification implements upstream.NotificationHandler
func (h *NotificationHandler) SendNotification(notification *upstream.Notification) {
	// Log the notification for debugging
	h.logger.Info("Tray notification",
		zap.String("level", notification.Level.String()),
		zap.String("title", notification.Title),
		zap.String("message", notification.Message),
		zap.String("server", notification.ServerName))

	// Show system notification using beeep
	// This works cross-platform on macOS, Windows, and Linux
	err := beeep.Notify(notification.Title, notification.Message, "")
	if err != nil {
		h.logger.Warnw("Failed to show system notification",
			"error", err,
			"title", notification.Title,
			"message", notification.Message)
	}
}

// ShowDockerRecoveryStarted shows a notification when Docker recovery starts
func ShowDockerRecoveryStarted() error {
	return beeep.Notify(
		"Docker Recovery",
		"Docker engine detected offline. Reconnecting servers...",
		"",
	)
}

// ShowDockerRecoverySuccess shows a notification when Docker recovery succeeds
func ShowDockerRecoverySuccess(serverCount int) error {
	msg := "All servers reconnected successfully"
	if serverCount > 0 {
		msg = fmt.Sprintf("Successfully reconnected %d server(s)", serverCount)
	}
	return beeep.Notify(
		"Recovery Complete",
		msg,
		"",
	)
}

// ShowDockerRecoveryFailed shows a notification when Docker recovery fails
func ShowDockerRecoveryFailed(reason string) error {
	msg := "Unable to reconnect servers. Check Docker status."
	if reason != "" {
		msg = fmt.Sprintf("Unable to reconnect servers: %s", reason)
	}
	return beeep.Notify(
		"Docker Recovery",
		msg,
		"",
	)
}
