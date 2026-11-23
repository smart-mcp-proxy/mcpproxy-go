package mcplogin

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestLimiterBurstRefillRetryAfter(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(clk.now)
	for i := 0; i < 5; i++ {
		if ok, _ := l.Allow(EndpointRegister, "1.2.3.4"); !ok {
			t.Fatalf("burst call %d refused", i)
		}
	}
	ok, retry := l.Allow(EndpointRegister, "1.2.3.4")
	if ok || retry < 1 {
		t.Fatalf("expected refusal with retry>=1, got %v %d", ok, retry)
	}
	if ok, _ := l.Allow(EndpointRegister, "5.6.7.8"); !ok {
		t.Fatal("other ip shares bucket")
	}
	if ok, _ := l.Allow(EndpointAuthorize, "1.2.3.4"); !ok {
		t.Fatal("other endpoint shares bucket")
	}
	clk.advance(6 * time.Second) // 10/min -> 1 per 6 s
	if ok, _ := l.Allow(EndpointRegister, "1.2.3.4"); !ok {
		t.Fatal("refill failed")
	}
	if ok, _ := l.Allow(EndpointRegister, "1.2.3.4"); ok {
		t.Fatal("only one token should have refilled")
	}
}

func TestLimiterDefaults(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	l := NewLimiter(clk.now)
	count := func(ep string) int {
		n := 0
		for {
			if ok, _ := l.Allow(ep, "9.9.9.9"); !ok {
				return n
			}
			n++
		}
	}
	if got := count(EndpointAuthorize); got != 10 {
		t.Errorf("authorize burst %d", got)
	}
	if got := count(EndpointToken); got != 20 {
		t.Errorf("token burst %d", got)
	}
	if ok, _ := l.Allow("unknown", "1.1.1.1"); !ok {
		t.Error("unknown endpoint must not be limited")
	}
}

func TestLimitConstants(t *testing.T) {
	if MaxBodyBytes != 16<<10 || MaxPendingRegistrations != 50 || MaxPendingRequests != 20 {
		t.Fatal("constants")
	}
}
