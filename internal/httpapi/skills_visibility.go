package httpapi

import (
	"context"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// skillVisibleTo is the one caller-class rule for held-skill review data
// (Spec 125 FR-019a, T031): the skill counts on review-queue rows, the
// skill_review queue rows and attention items, and the review.changed frame a
// skills change causes. Only an administrator (or a request carrying no
// AuthContext at all: the local socket and in-process callers) sees them.
// Every scoped caller does not: an agent token, pinned or not, and a
// non-administrator server-edition user session. Being in scope for the
// server is not enough.
func skillVisibleTo(ctx context.Context) bool {
	return !auth.IsScopedCaller(ctx)
}

// withoutHeldSkills narrows a review queue for a caller that may not see held
// skills: skill counts are removed, skill-only rows are dropped and the count
// excludes them. The rows are copied; the runtime's queue is not written.
func withoutHeldSkills(queue *internalRuntime.ReviewQueue) *internalRuntime.ReviewQueue {
	rows := make([]internalRuntime.ReviewQueueRow, 0, len(queue.Servers))
	for _, row := range queue.Servers {
		if row.Kind == internalRuntime.ReviewKindSkillReview {
			continue
		}
		row.SkillsPending, row.SkillsChanged = 0, 0
		rows = append(rows, row)
	}
	return &internalRuntime.ReviewQueue{Count: len(rows), Servers: rows}
}

// attentionKindVisibleTo reports whether an attention item of this kind may
// reach the caller at all, before the per-subject rule.
func attentionKindVisibleTo(ctx context.Context, kind string) bool {
	return kind != internalRuntime.AttentionKindSkillReview || skillVisibleTo(ctx)
}

// isSkillsReviewChange reports a review.changed frame caused by a skills change.
func isSkillsReviewChange(evt internalRuntime.Event) bool {
	if evt.Type != internalRuntime.EventTypeReviewChanged {
		return false
	}
	marked, _ := evt.Payload[internalRuntime.ReviewChangedSkillsKey].(bool)
	return marked
}
