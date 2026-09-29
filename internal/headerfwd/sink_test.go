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
