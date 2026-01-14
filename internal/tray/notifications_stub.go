//go:build nogui || headless || linux

package tray

import (
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream"

	"go.uber.org/zap"
)

// NotificationHandler implements upstream.NotificationHandler for headless builds (stub)
type NotificationHandler struct {
	logger *zap.SugaredLogger
}

// NewNotificationHandler creates a new tray notification handler (stub)
func NewNotificationHandler(logger *zap.SugaredLogger) *NotificationHandler {
	return &NotificationHandler{
		logger: logger,
	}
}

// SendNotification implements upstream.NotificationHandler (stub - just logs)
func (h *NotificationHandler) SendNotification(notification *upstream.Notification) {
	// Log the notification since no tray is available
	h.logger.Info("State change notification",
		zap.String("level", notification.Level.String()),
		zap.String("title", notification.Title),
		zap.String("message", notification.Message),
		zap.String("server", notification.ServerName))
}

// ShowDockerRecoveryStarted shows a notification when Docker recovery starts (stub)
func ShowDockerRecoveryStarted() error {
	// No-op for headless/linux builds
	return nil
}

// ShowDockerRecoverySuccess shows a notification when Docker recovery succeeds (stub)
func ShowDockerRecoverySuccess(serverCount int) error {
	// No-op for headless/linux builds
	return nil
}

// ShowDockerRecoveryFailed shows a notification when Docker recovery fails (stub)
func ShowDockerRecoveryFailed(reason string) error {
	// No-op for headless/linux builds
	return nil
}
