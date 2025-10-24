package transport

import "context"

// ConnectionSource identifies the origin of a connection
type ConnectionSource string

const (
	// ConnectionSourceTCP identifies connections from TCP listener (browsers, remote clients)
	ConnectionSourceTCP ConnectionSource = "tcp"
	// ConnectionSourceTray identifies connections from tray via Unix socket or named pipe
	ConnectionSourceTray ConnectionSource = "tray"
)

// Context key for connection source tagging
type contextKey string

const connSourceKey contextKey = "connection_source"

// TagConnectionContext tags a context with the connection source
func TagConnectionContext(ctx context.Context, source ConnectionSource) context.Context {
	return context.WithValue(ctx, connSourceKey, source)
}

// GetConnectionSource retrieves the connection source from context
func GetConnectionSource(ctx context.Context) ConnectionSource {
	if source, ok := ctx.Value(connSourceKey).(ConnectionSource); ok {
		return source
	}
	return ConnectionSourceTCP // Default to TCP (most restrictive)
}
