package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// TracingTransport wraps a transport.Interface to log raw JSON-RPC messages
type TracingTransport struct {
	inner      transport.Interface
	logger     *zap.Logger
	serverName string
	enabled    bool
}

// NewTracingTransport creates a new tracing transport wrapper
func NewTracingTransport(inner transport.Interface, logger *zap.Logger, serverName string, enabled bool) *TracingTransport {
	return &TracingTransport{
		inner:      inner,
		logger:     logger,
		serverName: serverName,
		enabled:    enabled,
	}
}

// Start implements transport.Interface
func (t *TracingTransport) Start(ctx context.Context) error {
	if t.enabled {
		t.logger.Info("🔗 MCP Transport starting",
			zap.String("server", t.serverName),
			zap.String("transport_type", fmt.Sprintf("%T", t.inner)))
	}

	start := time.Now()
	err := t.inner.Start(ctx)
	duration := time.Since(start)

	if t.enabled {
		if err != nil {
			t.logger.Error("❌ MCP Transport start failed",
				zap.String("server", t.serverName),
				zap.Error(err),
				zap.Duration("duration", duration))
		} else {
			t.logger.Info("✅ MCP Transport started successfully",
				zap.String("server", t.serverName),
				zap.Duration("duration", duration))
		}
	}

	return err
}

// Close implements transport.Interface
func (t *TracingTransport) Close() error {
	if t.enabled {
		t.logger.Info("🔌 MCP Transport closing",
			zap.String("server", t.serverName))
	}

	start := time.Now()
	err := t.inner.Close()
	duration := time.Since(start)

	if t.enabled {
		if err != nil {
			t.logger.Error("❌ MCP Transport close failed",
				zap.String("server", t.serverName),
				zap.Error(err),
				zap.Duration("duration", duration))
		} else {
			t.logger.Info("✅ MCP Transport closed successfully",
				zap.String("server", t.serverName),
				zap.Duration("duration", duration))
		}
	}

	return err
}

// SendRequest implements transport.Interface
func (t *TracingTransport) SendRequest(ctx context.Context, request transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	if t.enabled {
		// Log outgoing request
		if requestBytes, err := json.MarshalIndent(request, "", "  "); err == nil {
			t.logger.Info("📤 MCP Request: Client → Server",
				zap.String("server", t.serverName),
				zap.String("method", request.Method),
				zap.Any("id", request.ID),
				zap.String("message", string(requestBytes)))
		} else {
			t.logger.Info("📤 MCP Request: Client → Server",
				zap.String("server", t.serverName),
				zap.String("method", request.Method),
				zap.Any("id", request.ID),
				zap.Any("request", request))
		}
	}

	start := time.Now()
	response, err := t.inner.SendRequest(ctx, request)
	duration := time.Since(start)

	if t.enabled {
		if err != nil {
			t.logger.Error("❌ MCP Request failed",
				zap.String("server", t.serverName),
				zap.String("method", request.Method),
				zap.Any("id", request.ID),
				zap.Error(err),
				zap.Duration("duration", duration))
		} else {
			// Log incoming response
			if responseBytes, err := json.MarshalIndent(response, "", "  "); err == nil {
				t.logger.Info("📥 MCP Response: Server → Client",
					zap.String("server", t.serverName),
					zap.String("method", request.Method),
					zap.Any("id", response.ID),
					zap.String("message", string(responseBytes)),
					zap.Duration("duration", duration))
			} else {
				t.logger.Info("📥 MCP Response: Server → Client",
					zap.String("server", t.serverName),
					zap.String("method", request.Method),
					zap.Any("id", response.ID),
					zap.Any("response", response),
					zap.Duration("duration", duration))
			}
		}
	}

	return response, err
}

// SendNotification implements transport.Interface
func (t *TracingTransport) SendNotification(ctx context.Context, notification mcp.JSONRPCNotification) error {
	if t.enabled {
		// Log outgoing notification
		if notificationBytes, err := json.MarshalIndent(notification, "", "  "); err == nil {
			t.logger.Info("📤 MCP Notification: Client → Server",
				zap.String("server", t.serverName),
				zap.String("method", notification.Method),
				zap.String("message", string(notificationBytes)))
		} else {
			t.logger.Info("📤 MCP Notification: Client → Server",
				zap.String("server", t.serverName),
				zap.String("method", notification.Method),
				zap.Any("notification", notification))
		}
	}

	start := time.Now()
	err := t.inner.SendNotification(ctx, notification)
	duration := time.Since(start)

	if t.enabled {
		if err != nil {
			t.logger.Error("❌ MCP Notification failed",
				zap.String("server", t.serverName),
				zap.String("method", notification.Method),
				zap.Error(err),
				zap.Duration("duration", duration))
		} else {
			t.logger.Debug("✅ MCP Notification sent successfully",
				zap.String("server", t.serverName),
				zap.String("method", notification.Method),
				zap.Duration("duration", duration))
		}
	}

	return err
}

// SetNotificationHandler implements transport.Interface
func (t *TracingTransport) SetNotificationHandler(handler func(notification mcp.JSONRPCNotification)) {
	if t.enabled {
		t.logger.Debug("🔔 MCP Notification handler set",
			zap.String("server", t.serverName))
	}

	// Wrap the handler to trace incoming notifications
	wrappedHandler := func(notification mcp.JSONRPCNotification) {
		if t.enabled {
			// Log incoming notification
			if notificationBytes, err := json.MarshalIndent(notification, "", "  "); err == nil {
				t.logger.Info("📥 MCP Notification: Server → Client",
					zap.String("server", t.serverName),
					zap.String("method", notification.Method),
					zap.String("message", string(notificationBytes)))
			} else {
				t.logger.Info("📥 MCP Notification: Server → Client",
					zap.String("server", t.serverName),
					zap.String("method", notification.Method),
					zap.Any("notification", notification))
			}
		}

		// Call the original handler
		if handler != nil {
			handler(notification)
		}
	}

	t.inner.SetNotificationHandler(wrappedHandler)
}

// GetSessionId implements transport.Interface
func (t *TracingTransport) GetSessionId() string {
	return t.inner.GetSessionId()
}
