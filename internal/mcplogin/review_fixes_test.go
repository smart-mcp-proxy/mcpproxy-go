package mcplogin

import (
	"fmt"
	"testing"
	"time"
)

func (s *Store) size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reqs) + len(s.codes)
}

func (l *Limiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}

func TestMatchRedirectDiffersByPortOnly(t *testing.T) {
	reg := []string{"http://localhost/cb"}
	for _, in := range []string{
		"http://localhost:54321/cb?",
		"HTTP://localhost:54321/cb",
		"http://LOCALHOST:54321/cb",
		"http://localhost:54321/CB",
	} {
		if got, ok := MatchRedirect(reg, in); ok {
			t.Errorf("%q matched as %q", in, got)
		}
	}
	if _, ok := MatchRedirect([]string{"http://localhost:1/cb"}, "http://localhost:54321/cb"); !ok {
		t.Error("registered port must be ignored too")
	}
	if _, ok := MatchRedirect([]string{"http://[::1]/cb"}, "http://[::1]:5/cb"); !ok {
		t.Error("ipv6 port")
	}
}

func TestStoreStaysBounded(t *testing.T) {
	s, clk := newTestStore()
	for round := 0; round < 5; round++ {
		for i := 0; i < MaxPendingRequests; i++ {
			mustCreate(t, s, "")
		}
		clk.advance(RequestLifetime + time.Second)
	}
	if n := s.size(); n > MaxPendingRequests {
		t.Fatalf("store grew to %d entries", n)
	}
}

func TestStoreCodesStayBounded(t *testing.T) {
	s, clk := newTestStore()
	for round := 0; round < 5; round++ {
		r := mustCreate(t, s, "")
		code, _ := s.Approve(r.ID, Decision{ClientID: "c"})
		clk.advance(RequestLifetime + TombstoneLifetime)
		_, _, _ = s.Consume(code, nil)
	}
	mustCreate(t, s, "")
	if n := s.size(); n > 3 {
		t.Fatalf("store grew to %d entries", n)
	}
}

func TestLimiterBoundedKeys(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(clk.now)
	for i := 0; i < MaxTrackedKeys; i++ {
		if ok, _ := l.Allow(EndpointToken, fmt.Sprintf("ip-%d", i)); !ok {
			t.Fatalf("key %d refused", i)
		}
	}
	ok, retry := l.Allow(EndpointToken, "newcomer")
	if ok || retry < 1 {
		t.Fatalf("full limiter must refuse new key: %v %d", ok, retry)
	}
	if ok, _ := l.Allow(EndpointToken, "ip-0"); !ok {
		t.Fatal("known key must still be served")
	}
	clk.advance(2 * time.Minute)
	if ok, _ := l.Allow(EndpointToken, "newcomer"); !ok {
		t.Fatal("idle keys should have been pruned")
	}
	if n := l.tracked(); n > 2 {
		t.Fatalf("tracked %d", n)
	}
}
