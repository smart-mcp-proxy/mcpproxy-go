package headerfwd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
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

// TextScrubber is implemented by typed errors that carry upstream text (for
// example transport.HTTPError's Body). ScrubbedCopy returns a copy with every
// text field passed through scrub; it must not modify the receiver.
type TextScrubber interface {
	ScrubbedCopy(scrub func(string) string) error
}

// scrubbedError carries scrubbed text. It deliberately has no Unwrap: that
// would hand the raw error (and its unscrubbed text) to any caller that walks
// the chain. Classification still works through Is and As, and As swaps a
// text-carrying typed error for its scrubbed copy.
type scrubbedError struct {
	msg   string
	err   error
	scrub func(string) string
}

func (e *scrubbedError) Error() string { return e.msg }

// Is reports whether the original chain matches target. It returns only a
// bool, so no text escapes.
func (e *scrubbedError) Is(target error) bool { return errors.Is(e.err, target) }

// As finds target in the original chain. A match that implements TextScrubber
// is replaced with its scrubbed copy. Any other match whose text still carries
// a forwarded value (for example a wrapper whose cause echoes it) is refused:
// the target is reset and As reports no match.
func (e *scrubbedError) As(target any) bool {
	if !errors.As(e.err, target) {
		return false
	}
	v := reflect.ValueOf(target).Elem()
	if ts, ok := v.Interface().(TextScrubber); ok {
		if c := reflect.ValueOf(ts.ScrubbedCopy(e.scrub)); c.IsValid() && c.Type().AssignableTo(v.Type()) {
			v.Set(c)
			return true
		}
	}
	if e.carriesValue(v.Interface()) {
		v.Set(reflect.Zero(v.Type()))
		return false
	}
	return true
}

// carriesValue reports whether a non-scrubbable As match exposes a forwarded
// value anywhere reachable: its Error() text, its Go-syntax fields (%#v), or
// any error in its Unwrap chain (single or joined).
func (e *scrubbedError) carriesValue(x any) bool {
	dirty := func(t string) bool { return e.scrub(t) != t }
	if fieldsCarry(reflect.ValueOf(x), dirty, 0) {
		return true
	}
	err, ok := x.(error)
	if !ok || err == nil {
		return false
	}
	seen := 0
	var walk func(error) bool
	walk = func(err error) bool {
		if err == nil || seen > 64 {
			return false
		}
		seen++
		if dirty(err.Error()) || fieldsCarry(reflect.ValueOf(err), dirty, 0) {
			return true
		}
		switch u := err.(type) {
		case interface{ Unwrap() error }:
			return walk(u.Unwrap())
		case interface{ Unwrap() []error }:
			for _, c := range u.Unwrap() {
				if walk(c) {
					return true
				}
			}
		}
		return false
	}
	return walk(err)
}

// Format prints only the scrubbed text for every verb, so %+v / %#v never
// reach the struct fields.
func (e *scrubbedError) Format(f fmt.State, verb rune) {
	if verb == 'q' {
		fmt.Fprintf(f, "%q", e.msg)
		return
	}
	_, _ = io.WriteString(f, e.msg)
}

// ScrubError returns err unchanged when s is empty or nothing matched;
// otherwise an error whose text is scrubbed. errors.Is keeps working, and
// errors.As returns scrubbed copies of text-carrying typed errors.
func ScrubError(err error, s Snapshot, allow []string) error {
	if err == nil || s.IsEmpty() {
		return err
	}
	msg := err.Error()
	clean := Scrub(msg, s, allow)
	if clean == msg {
		return err
	}
	return &scrubbedError{msg: clean, err: err, scrub: func(t string) string { return Scrub(t, s, allow) }}
}

// fieldsCarry walks v's string data directly through reflect (unexported
// fields included), so a custom Format or GoString method cannot hide a raw
// value. Depth-bounded; maps, slices and interfaces are followed.
func fieldsCarry(v reflect.Value, dirty func(string) bool, depth int) bool {
	if !v.IsValid() || depth > 6 {
		return false
	}
	switch v.Kind() {
	case reflect.String:
		return dirty(v.String())
	case reflect.Pointer, reflect.Interface:
		return !v.IsNil() && fieldsCarry(v.Elem(), dirty, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if fieldsCarry(v.Field(i), dirty, depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
			return dirty(string(v.Bytes()))
		}
		for i := 0; i < v.Len() && i < 256; i++ {
			if fieldsCarry(v.Index(i), dirty, depth+1) {
				return true
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for n := 0; it.Next() && n < 256; n++ {
			if fieldsCarry(it.Key(), dirty, depth+1) || fieldsCarry(it.Value(), dirty, depth+1) {
				return true
			}
		}
	}
	return false
}
