package callerr

// note.go: the per-call mutable companion that carries a classification from
// the dispatch site to the activity completion funnel. The funnel receives
// only prose (errorMsg), so the dispatch path notes the typed outcome on the
// request context and the funnel reads it back, the same shape the Spec 107
// audit attempt uses (internal/server/audit_funnel.go).

import (
	"context"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	proxytransport "github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

type noteKeyType struct{}

var noteKey noteKeyType

// Note is the per-call classification holder.
type Note struct {
	mu      sync.Mutex
	rec     *proxytransport.CallRecorder
	outcome Outcome
	set     bool
}

// WithNote returns a ctx carrying a FRESH note. It deliberately shadows any
// note already on ctx: a code_execution sub-call is derived from its parent's
// context but is a separate call with its own outcome.
func WithNote(ctx context.Context) context.Context {
	return context.WithValue(ctx, noteKey, &Note{})
}

func noteFrom(ctx context.Context) *Note {
	if ctx == nil {
		return nil
	}
	n, _ := ctx.Value(noteKey).(*Note)
	return n
}

// BeginDispatch returns the ctx to hand to the upstream call: it carries a
// transport call recorder (FR-042), which is also remembered on the note so
// Observe can read its facts.
func BeginDispatch(ctx context.Context) context.Context {
	dctx, rec := proxytransport.WithCallRecorder(ctx)
	if n := noteFrom(ctx); n != nil {
		n.mu.Lock()
		n.rec = rec
		n.mu.Unlock()
	}
	return dctx
}

// Observe classifies a finished dispatch and notes the outcome. result is the
// manager's untyped return; only an mcp.CallToolResult (pointer or value) is read. A success
// clears any earlier outcome. A no-op when ctx carries no note.
func Observe(ctx context.Context, result interface{}, err error) {
	n := noteFrom(ctx)
	if n == nil {
		return
	}
	var res *mcp.CallToolResult
	switch v := result.(type) {
	case *mcp.CallToolResult:
		res = v
	case mcp.CallToolResult:
		res = &v
	}
	n.mu.Lock()
	facts := FactsFromRecorder(n.rec.Snapshot(), ctx.Err() == context.Canceled)
	n.mu.Unlock()
	o, failed := Classify(res, err, facts)
	n.mu.Lock()
	n.outcome, n.set = o, failed
	n.mu.Unlock()
}

// NoteOutcome records an explicit outcome for paths that fail without a typed
// error (a server with no client, rejected arguments). A no-op without a note.
func NoteOutcome(ctx context.Context, o Outcome) {
	if n := noteFrom(ctx); n != nil {
		n.mu.Lock()
		n.outcome, n.set = o, true
		n.mu.Unlock()
	}
}

// OutcomeFrom returns the outcome noted on ctx, if any.
func OutcomeFrom(ctx context.Context) (Outcome, bool) {
	n := noteFrom(ctx)
	if n == nil {
		return Outcome{}, false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.outcome, n.set
}
