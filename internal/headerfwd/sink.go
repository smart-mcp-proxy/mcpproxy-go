package headerfwd

import (
	"context"
	"sync"
)

// Sink is a per-call out-parameter. The server layer creates one for a single
// upstream call, core.Client.CallTool records that call's outbound set into it,
// and the recording code reads it back to scrub the result copies it writes to
// activity and tool-call records (FR-016.3). It is owned by exactly one call,
// never stored on a long-lived object (FR-011), and prints names only.
type Sink struct {
	mu  sync.Mutex
	out Snapshot
}

type ctxKeySink struct{}

// WithSink returns a context carrying a fresh Sink for one upstream call.
func WithSink(ctx context.Context) (context.Context, *Sink) {
	k := &Sink{}
	return context.WithValue(ctx, ctxKeySink{}, k), k
}

// SinkFrom returns the Sink carried by ctx, or nil.
func SinkFrom(ctx context.Context) *Sink {
	k, _ := ctx.Value(ctxKeySink{}).(*Sink)
	return k
}

// RecordOutbound stores the call's outbound set in the Sink carried by ctx, if
// any. It is a no-op without one.
func RecordOutbound(ctx context.Context, s Snapshot) {
	k, _ := ctx.Value(ctxKeySink{}).(*Sink)
	if k == nil {
		return
	}
	k.mu.Lock()
	k.out = s
	k.mu.Unlock()
}

// Merge unions o into the Sink. code_execution uses it to fold every nested
// sub-call's outbound set into one execution-level set, so the execution's own
// records can be scrubbed with everything any sub-call forwarded.
func (k *Sink) Merge(o Snapshot) {
	if k == nil || o.IsEmpty() {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	m := make(map[string]string, len(k.out.h)+len(o.h))
	for n, v := range k.out.h {
		m[n] = v
	}
	for n, v := range o.h {
		m[n] = v
	}
	k.out = newSnapshot(m)
}

// Outbound returns the recorded outbound set (empty when nothing was
// forwarded or the call never reached core.Client.CallTool). Nil-safe.
func (k *Sink) Outbound() Snapshot {
	if k == nil {
		return Snapshot{}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.out
}

// String never prints values.
func (k *Sink) String() string { return "forward_sink{" + k.Outbound().String() + "}" }

// scrubbedError carries scrubbed text while keeping the wrapped chain, so
// errors.Is/As classification upstream of core.Client.CallTool still works.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// ScrubError returns err unchanged when s is empty or nothing matched;
// otherwise an error whose text is scrubbed and whose chain is preserved.
func ScrubError(err error, s Snapshot, allow []string) error {
	if err == nil || s.IsEmpty() {
		return err
	}
	msg := err.Error()
	clean := Scrub(msg, s, allow)
	if clean == msg {
		return err
	}
	return &scrubbedError{msg: clean, err: err}
}
