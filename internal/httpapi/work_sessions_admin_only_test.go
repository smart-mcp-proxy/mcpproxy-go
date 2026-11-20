package httpapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 FR-019a on the Spec 124 timeline door: an administrator-only row
// type is never a scoped caller's row, even when it carries that caller's
// identity; an administrator still reads it.
func TestWorkSessionReach_AdminOnlyTypeIsNotAScopedCallersRow(t *testing.T) {
	rec := &storage.ActivityRecord{Type: storage.ActivityTypeSkillQuarantineChange, UserID: "u1"}
	own := &storage.ActivityRecord{Type: storage.ActivityTypeToolCall, UserID: "u1"}

	scoped := workSessionReach{scoped: true, ac: &auth.AuthContext{UserID: "u1"}, ctx: context.Background()}
	assert.True(t, scoped.ownsRow(own), "the caller's own call row is served")
	assert.False(t, scoped.ownsRow(rec), "a skill approval row is administrator-only")

	admin := workSessionReach{ctx: context.Background()}
	assert.True(t, admin.ownsRow(rec))
}
