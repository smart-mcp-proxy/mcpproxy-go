package mcplogin

import (
	"errors"
	"fmt"
	"sync"
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

func TestApprovedCodeLivesFullLifetime(t *testing.T) {
	s, clk := newTestStore()
	r := mustCreate(t, s, "")
	clk.advance(RequestLifetime - 10*time.Second)
	code, err := s.Approve(r.ID, Decision{ClientID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	clk.advance(50 * time.Second)
	if _, _, err := s.Consume(code, func(Request) (string, error) { return "g", nil }); err != nil {
		t.Fatalf("code died early: %v", err)
	}
	r2 := mustCreate(t, s, "")
	code2, _ := s.Approve(r2.ID, Decision{ClientID: "c"})
	clk.advance(CodeLifetime + time.Second)
	if _, _, err := s.Consume(code2, func(Request) (string, error) { return "g", nil }); !errors.Is(err, ErrCodeUnknown) {
		t.Fatalf("expired code: %v", err)
	}
}

func TestLimiterBucketsStayBoundedConcurrently(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(clk.now)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < MaxTrackedKeys; i++ {
				l.Allow(EndpointToken, fmt.Sprintf("ip-%d-%d", g, i))
				if i%500 == 0 {
					clk.advance(30 * time.Second)
				}
			}
		}(g)
	}
	wg.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) > MaxTrackedKeys || l.rates.Len() > MaxTrackedKeys {
		t.Fatalf("seen %d buckets %d", len(l.seen), l.rates.Len())
	}
	if l.rates.Len() > len(l.seen) {
		t.Fatalf("bucket without seen entry: seen %d buckets %d", len(l.seen), l.rates.Len())
	}
}
