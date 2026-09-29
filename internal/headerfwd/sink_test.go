package headerfwd

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func snapWith(t *testing.T, name, value string) Snapshot {
	t.Helper()
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set(name, value)
	s := Capture(r, map[string]struct{}{name: {}})
	if s.IsEmpty() {
		t.Fatal("capture produced an empty snapshot")
	}
	return s
}

func TestSinkPerCall(t *testing.T) {
	ctx1, k1 := WithSink(context.Background())
	_, k2 := WithSink(context.Background())
	RecordOutbound(ctx1, snapWith(t, "X-Tenant-Id", "tenant-secret-1"))
	if k1.Outbound().Len() != 1 || !k2.Outbound().IsEmpty() {
		t.Fatalf("sinks must be independent: %v %v", k1.Outbound(), k2.Outbound())
	}
	RecordOutbound(context.Background(), Snapshot{}) // no sink: no-op, no panic
	var nilSink *Sink
	if !nilSink.Outbound().IsEmpty() {
		t.Fatal("nil sink must be empty")
	}
	if strings.Contains(fmt.Sprint(k1), "tenant-secret-1") {
		t.Fatal("sink formatting leaked a value")
	}
}

func TestScrubErrorKeepsChain(t *testing.T) {
	s := snapWith(t, "X-Tenant-Id", "tenant-secret-1")
	base := errors.New("boom")
	err := fmt.Errorf("upstream said tenant-secret-1: %w", base)
	got := ScrubError(err, s, []string{"X-Tenant-Id"})
	if strings.Contains(got.Error(), "tenant-secret-1") {
		t.Fatalf("value survived: %v", got)
	}
	if !errors.Is(got, base) {
		t.Fatal("wrapped chain must survive scrubbing")
	}
	if ScrubError(base, s, nil) != base {
		t.Fatal("no match must return the original error")
	}
	if ScrubError(err, Snapshot{}, nil) != err || ScrubError(nil, s, nil) != nil {
		t.Fatal("empty snapshot / nil error passthrough")
	}
}

func TestSinkMerge(t *testing.T) {
	_, k := WithSink(context.Background())
	k.Merge(snapWith(t, "X-Tenant-Id", "tenant-secret-1"))
	k.Merge(snapWith(t, "X-Region", "eu-west"))
	k.Merge(Snapshot{})
	var nilSink *Sink
	nilSink.Merge(snapWith(t, "X-Region", "eu-west")) // no panic
	if got := k.Outbound().Names(); len(got) != 2 || got[0] != "X-Region" || got[1] != "X-Tenant-Id" {
		t.Fatalf("merge = %v", got)
	}
}

// textErr is a typed error carrying upstream text, like transport.HTTPError.
type textErr struct{ Body string }

func (e *textErr) Error() string { return "HTTP 500: " + e.Body }

func (e *textErr) ScrubbedCopy(scrub func(string) string) error {
	return &textErr{Body: scrub(e.Body)}
}

type plainTypedErr struct{ Code int }

func (e *plainTypedErr) Error() string { return fmt.Sprintf("code %d", e.Code) }

// Review round 9 (codex gpt-6-luna): the scrubbed wrapper must not hand the
// raw error back through Unwrap / errors.As.
func TestScrubErrorChainDoesNotExposeRawValue(t *testing.T) {
	s := snapWith(t, "X-Tenant-Id", "tenant-secret-1")
	raw := &textErr{Body: "echo tenant-secret-1"}
	got := ScrubError(fmt.Errorf("call failed: %w", raw), s, []string{"X-Tenant-Id"})

	for u := errors.Unwrap(got); u != nil; u = errors.Unwrap(u) {
		if strings.Contains(u.Error(), "tenant-secret-1") {
			t.Fatalf("Unwrap exposed the raw value: %v", u)
		}
	}
	var te *textErr
	if !errors.As(got, &te) {
		t.Fatal("typed error must stay reachable via errors.As")
	}
	if strings.Contains(te.Body, "tenant-secret-1") || te == raw {
		t.Fatalf("errors.As returned the raw typed error: %q", te.Body)
	}

	// Typed errors without text keep classification unchanged.
	pe := &plainTypedErr{Code: 7}
	got = ScrubError(fmt.Errorf("tenant-secret-1: %w", pe), s, nil)
	var gotPE *plainTypedErr
	if !errors.As(got, &gotPE) || gotPE.Code != 7 {
		t.Fatal("non-text typed error must stay reachable via errors.As")
	}
}

// Review round 10: no fmt verb and no wrapper-shaped errors.As target may
// reach the raw chain.
func TestScrubErrorNoRawViaFormatOrWrapperAs(t *testing.T) {
	s := snapWith(t, "X-Tenant-Id", "tenant-secret-1")
	raw := fmt.Errorf("inner tenant-secret-1: %w", errors.New("base"))
	got := ScrubError(fmt.Errorf("outer: %w", raw), s, []string{"X-Tenant-Id"})

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if out := fmt.Sprintf(verb, got); strings.Contains(out, "tenant-secret-1") {
			t.Fatalf("%s leaked: %s", verb, out)
		}
	}
	var w interface{ Unwrap() error }
	if errors.As(got, &w) {
		t.Fatalf("wrapper-shaped As target reached the raw chain: %v", w)
	}
}
