package headerfwd

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"go.uber.org/zap/zapcore"
)

// Snapshot is an immutable set of canonical header name -> value. Every
// formatting path prints names and a count only (FR-007).
type Snapshot struct{ h map[string]string }

func newSnapshot(h map[string]string) Snapshot {
	if len(h) == 0 {
		return Snapshot{}
	}
	return Snapshot{h: h}
}

// Len is the number of headers in the snapshot.
func (s Snapshot) Len() int { return len(s.h) }

// IsEmpty reports whether the snapshot holds no headers.
func (s Snapshot) IsEmpty() bool { return len(s.h) == 0 }

// Names returns the sorted canonical names.
func (s Snapshot) Names() []string {
	out := make([]string, 0, len(s.h))
	for k := range s.h {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Has reports whether name (any case) is present.
func (s Snapshot) Has(name string) bool {
	_, ok := s.h[canon(name)]
	return ok
}

func (s Snapshot) String() string {
	return fmt.Sprintf("forwarded_headers{names=%v n=%d}", s.Names(), len(s.h))
}

// GoString implements fmt.GoStringer.
func (s Snapshot) GoString() string { return s.String() }

// Format implements fmt.Formatter so no verb can print the values.
func (s Snapshot) Format(f fmt.State, _ rune) { _, _ = f.Write([]byte(s.String())) }

// MarshalJSON emits names and count only.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Names []string `json:"names"`
		N     int      `json:"n"`
	}{s.Names(), len(s.h)})
}

// LogValue implements slog.LogValuer.
func (s Snapshot) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalLogObject implements zapcore.ObjectMarshaler.
func (s Snapshot) MarshalLogObject(enc zapcore.ObjectEncoder) error {
	_ = enc.AddArray("names", zapcore.ArrayMarshalerFunc(func(ae zapcore.ArrayEncoder) error {
		for _, n := range s.Names() {
			ae.AppendString(n)
		}
		return nil
	}))
	enc.AddInt("n", len(s.h))
	return nil
}

type ctxKeyA struct{}
type ctxKeyB struct{}

// WithSnapshot stores the edge snapshot under key A.
func WithSnapshot(ctx context.Context, s Snapshot) context.Context {
	return context.WithValue(ctx, ctxKeyA{}, s)
}

// SnapshotFrom reads key A.
func SnapshotFrom(ctx context.Context) (Snapshot, bool) {
	s, ok := ctx.Value(ctxKeyA{}).(Snapshot)
	return s, ok
}

// WithOutbound stores the per-server outbound set under key B. Only
// core.Client.CallTool should call this.
func WithOutbound(ctx context.Context, s Snapshot) context.Context {
	return context.WithValue(ctx, ctxKeyB{}, s)
}

// OutboundFrom reads key B.
func OutboundFrom(ctx context.Context) (Snapshot, bool) {
	s, ok := ctx.Value(ctxKeyB{}).(Snapshot)
	return s, ok
}
