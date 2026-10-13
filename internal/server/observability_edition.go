//go:build !server

package server

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
)

// editionToolCallAttributes returns edition-specific span attributes for a tool
// call. The personal edition is single-user and adds none (MCP-32).
func editionToolCallAttributes(_ context.Context, _ string) []attribute.KeyValue {
	return nil
}
