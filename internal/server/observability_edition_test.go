//go:build !server

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
)

// MCP-3207 negative control: the personal edition is single-user and MUST NOT
// emit user_id / profile telemetry labels, regardless of any auth context that
// happens to be present (MCP-32, internal/server/observability_edition.go).
func TestEditionToolCallAttributes_PersonalEditionEmitsNoLabels(t *testing.T) {
	// Even with a fully-populated user auth context and a non-default profile,
	// the personal-edition stub returns no edition attributes.
	ctx := auth.WithAuthContext(context.Background(),
		auth.UserContext("01HZUSERAAAAAAAAAAAAAAAAA", "alice@example.com", "Alice", "google"))

	attrs := editionToolCallAttributes(ctx, "team-acme")
	assert.Empty(t, attrs, "personal edition must not attach user_id/profile telemetry attributes")
}
