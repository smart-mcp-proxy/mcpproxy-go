//go:build !nogui && !headless && !linux

package tray

import (
	"mcpproxy-go/internal/upstream"

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

	// For now, we just log the notification
	// In a future implementation, we could:
	// 1. Show system tray notifications/balloons
	// 2. Update tray icon/tooltip to reflect current status
	// 3. Add notification history to tray menu

	// Note: Actual tray notification display would depend on OS capabilities
	// and the systray library's notification features
}
