package server

import (
	"context"
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/profile"
)

type profileToolCapture struct {
	mu  sync.Mutex
	err *profile.ToolBlockedError
}

type profileToolCaptureKeyType struct{}

var profileToolCaptureKey profileToolCaptureKeyType

func withProfileToolCapture(ctx context.Context) (context.Context, *profileToolCapture) {
	box := &profileToolCapture{}
	return context.WithValue(ctx, profileToolCaptureKey, box), box
}

func recordProfileToolRefusal(ctx context.Context, refusal *profile.ToolBlockedError) {
	if ctx == nil || refusal == nil {
		return
	}
	box, ok := ctx.Value(profileToolCaptureKey).(*profileToolCapture)
	if !ok || box == nil {
		return
	}
	box.mu.Lock()
	box.err = refusal
	box.mu.Unlock()
}

func (c *profileToolCapture) take() *profile.ToolBlockedError {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}
