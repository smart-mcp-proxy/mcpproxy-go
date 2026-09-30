package headerfwd

import (
	"context"
	"testing"
)

func TestOutbound(t *testing.T) {
	s := newSnapshot(map[string]string{"X-User-Id": "alice", "X-Tenant-Id": "t1", "X-Key": "client"})
	base := Policy{Enabled: true, Allow: []string{"x-user-id"}, Transport: "streamable-http"}

	if got := Outbound(s, base); got.Len() != 1 || got.h["X-User-Id"] != "alice" {
		t.Errorf("subset: %v", got)
	}
	p := base
	p.Enabled = false
	if !Outbound(s, p).IsEmpty() {
		t.Error("disabled must be empty")
	}
	for _, tr := range []string{"stdio", "sse", "", "auto"} {
		p = base
		p.Transport = tr
		if !Outbound(s, p).IsEmpty() {
			t.Errorf("transport %q must be empty", tr)
		}
	}
	p = base
	p.Transport = "http"
	if Outbound(s, p).Len() != 1 {
		t.Error("http transport should forward")
	}
	p = base
	p.Allow = []string{"X-Key", "X-Tenant-Id", "Authorization", "X-Missing"}
	p.Static = map[string]string{"x-key": "static"}
	if got := Outbound(s, p); got.Len() != 1 || !got.Has("X-Tenant-Id") {
		t.Errorf("static collision / deny: %v", got.Names())
	}
}

func TestHeaderFunc(t *testing.T) {
	s := newSnapshot(map[string]string{"X-User-Id": "alice"})
	f := HeaderFunc(nil)
	if f(context.Background()) != nil {
		t.Error("no keys -> nil")
	}
	if f(WithSnapshot(context.Background(), s)) != nil {
		t.Error("key A only -> nil")
	}
	ctx := WithOutbound(context.Background(), s)
	m := f(ctx)
	if m["X-User-Id"] != "alice" || len(m) != 1 {
		t.Fatalf("m = %v", m)
	}
	m["X-User-Id"] = "mutated"
	if f(ctx)["X-User-Id"] != "alice" {
		t.Error("map must be fresh per call")
	}
	// Re-applies deny list and static collision even for a hand-built set.
	bad := newSnapshot(map[string]string{"Authorization": "Bearer x", "Host": "h", "X-Key": "c", "X-Ok": "1"})
	got := HeaderFunc(map[string]string{"X-KEY": "s"})(WithOutbound(context.Background(), bad))
	if len(got) != 1 || got["X-Ok"] != "1" {
		t.Errorf("re-filter: %v", got)
	}
	if HeaderFunc(nil)(WithOutbound(context.Background(), Snapshot{})) != nil {
		t.Error("empty -> nil")
	}
}
