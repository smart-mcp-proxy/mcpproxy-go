package mcplogin

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestStore() (*Store, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	return NewStore(testKey, clk.now), clk
}

func mkReq(pathClient string) Request {
	return Request{RegistrationID: "reg1", RedirectURI: "http://127.0.0.1:1/cb", State: "st",
		CodeChallenge: "ch", Resource: "http://127.0.0.1:8080/mcp/oauth", Issuer: "http://127.0.0.1:8080", PathClient: pathClient}
}

func mustCreate(t *testing.T, s *Store, pc string) Request {
	t.Helper()
	r, err := s.Create(mkReq(pc))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCreateGetList(t *testing.T) {
	s, clk := newTestStore()
	r := mustCreate(t, s, "")
	if len(r.ID) != 22 || r.Status != StatusPending || !r.ExpiresAt.Equal(clk.now().Add(RequestLifetime)) {
		t.Fatalf("%+v", r)
	}
	g, ok := s.Get(r.ID)
	if !ok || g.ID != r.ID {
		t.Fatal("get")
	}
	if len(s.ListPending()) != 1 {
		t.Fatal("list")
	}
	clk.advance(RequestLifetime)
	if _, ok := s.Get(r.ID); ok {
		t.Fatal("expired must read as not found")
	}
	if len(s.ListPending()) != 0 {
		t.Fatal("expired listed")
	}
	if _, err := s.Approve(r.ID, Decision{ClientID: "c"}); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("approve expired: %v", err)
	}
}

func TestCap(t *testing.T) {
	s, _ := newTestStore()
	for i := 0; i < MaxPendingRequests; i++ {
		mustCreate(t, s, "")
	}
	if _, err := s.Create(mkReq("")); !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("%v", err)
	}
}

func TestCapIgnoresDecided(t *testing.T) {
	s, _ := newTestStore()
	var first Request
	for i := 0; i < MaxPendingRequests; i++ {
		r := mustCreate(t, s, "")
		if i == 0 {
			first = r
		}
	}
	if err := s.Deny(first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(mkReq("")); err != nil {
		t.Fatalf("slot should be free: %v", err)
	}
}

func TestDecidedOnceRace(t *testing.T) {
	s, _ := newTestStore()
	r := mustCreate(t, s, "")
	var wins, losses int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = s.Approve(r.ID, Decision{ClientID: "c"})
			} else {
				err = s.Deny(r.ID)
			}
			if err == nil {
				atomic.AddInt32(&wins, 1)
			} else if errors.Is(err, ErrAlreadyDecided) {
				atomic.AddInt32(&losses, 1)
			} else {
				t.Errorf("unexpected %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 || losses != 15 {
		t.Fatalf("wins %d losses %d", wins, losses)
	}
}

func TestApproveConsumeHappyPath(t *testing.T) {
	s, _ := newTestStore()
	r := mustCreate(t, s, "cursor")
	code, err := s.Approve(r.ID, Decision{ClientID: "cursor", ActorKind: "admin", ActorName: "a"})
	if err != nil || code == "" {
		t.Fatal(err)
	}
	g, _ := s.Get(r.ID)
	if g.Status != StatusApproved || g.Decision.ClientID != "cursor" {
		t.Fatalf("%+v", g)
	}
	if _, err := s.Approve(r.ID, Decision{}); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatal(err)
	}
	if err := s.Deny(r.ID); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatal(err)
	}
	var seen Request
	got, gid, err := s.Consume(code, func(q Request) (string, error) { seen = q; return "grant1", nil })
	if err != nil || gid != "grant1" || got.ID != r.ID || seen.Decision.ClientID != "cursor" {
		t.Fatalf("%+v %q %v", got, gid, err)
	}
	if g, _ := s.Get(r.ID); g.Status != StatusConsumed {
		t.Fatalf("status %s", g.Status)
	}
}

func TestConsumeReuse(t *testing.T) {
	s, clk := newTestStore()
	r := mustCreate(t, s, "")
	code, _ := s.Approve(r.ID, Decision{ClientID: "c"})
	calls := 0
	commit := func(Request) (string, error) { calls++; return "g1", nil }
	if _, _, err := s.Consume(code, commit); err != nil {
		t.Fatal(err)
	}
	clk.advance(5 * time.Minute)
	_, gid, err := s.Consume(code, commit)
	if !errors.Is(err, ErrCodeReused) || gid != "g1" || calls != 1 {
		t.Fatalf("%v %q %d", err, gid, calls)
	}
	clk.advance(6 * time.Minute)
	if _, gid, err := s.Consume(code, commit); !errors.Is(err, ErrCodeUnknown) || gid != "" {
		t.Fatalf("%v %q", err, gid)
	}
}

func TestConsumeExpiryAndUnknown(t *testing.T) {
	s, clk := newTestStore()
	r := mustCreate(t, s, "")
	code, _ := s.Approve(r.ID, Decision{ClientID: "c"})
	if _, _, err := s.Consume("nope", nil); !errors.Is(err, ErrCodeUnknown) {
		t.Fatal(err)
	}
	clk.advance(CodeLifetime)
	called := false
	if _, _, err := s.Consume(code, func(Request) (string, error) { called = true; return "g", nil }); !errors.Is(err, ErrCodeUnknown) || called {
		t.Fatalf("%v %v", err, called)
	}
}

func TestConsumeCommitErrorBurnsCode(t *testing.T) {
	s, _ := newTestStore()
	r := mustCreate(t, s, "")
	code, _ := s.Approve(r.ID, Decision{ClientID: "c"})
	boom := errors.New("boom")
	if _, _, err := s.Consume(code, func(Request) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	called := false
	_, gid, err := s.Consume(code, func(Request) (string, error) { called = true; return "g", nil })
	if called || gid != "" || !errors.Is(err, ErrCodeReused) {
		t.Fatalf("code reusable after failed commit: %v %q %v", err, gid, called)
	}
}

func TestCancelForClient(t *testing.T) {
	s, _ := newTestStore()
	pathReq := mustCreate(t, s, "cursor")
	decided := mustCreate(t, s, "")
	code, _ := s.Approve(decided.ID, Decision{ClientID: "cursor"})
	other := mustCreate(t, s, "other")
	if n := s.CancelForClient("cursor"); n != 2 {
		t.Fatalf("cancelled %d", n)
	}
	if g, _ := s.Get(pathReq.ID); g.Status != StatusCancelled {
		t.Fatal("path req")
	}
	if g, _ := s.Get(decided.ID); g.Status != StatusCancelled {
		t.Fatal("decided req")
	}
	if g, _ := s.Get(other.ID); g.Status != StatusPending {
		t.Fatal("other touched")
	}
	called := false
	if _, _, err := s.Consume(code, func(Request) (string, error) { called = true; return "g", nil }); !errors.Is(err, ErrCodeUnknown) || called {
		t.Fatalf("cancelled code usable: %v", err)
	}
	if _, err := s.Approve(pathReq.ID, Decision{ClientID: "x"}); !errors.Is(err, ErrAlreadyDecided) {
		t.Fatalf("approve cancelled: %v", err)
	}
}

func TestCancelAllAndAfterConsume(t *testing.T) {
	s, _ := newTestStore()
	a := mustCreate(t, s, "")
	b := mustCreate(t, s, "x")
	code, _ := s.Approve(b.ID, Decision{ClientID: "x"})
	consumedReq := mustCreate(t, s, "")
	c2, _ := s.Approve(consumedReq.ID, Decision{ClientID: "y"})
	if _, _, err := s.Consume(c2, func(Request) (string, error) { return "g", nil }); err != nil {
		t.Fatal(err)
	}
	if n := s.CancelAll(); n != 2 {
		t.Fatalf("cancelled %d", n)
	}
	if g, _ := s.Get(a.ID); g.Status != StatusCancelled {
		t.Fatal("a")
	}
	if _, _, err := s.Consume(code, func(Request) (string, error) { return "g", nil }); !errors.Is(err, ErrCodeUnknown) {
		t.Fatal(err)
	}
	if _, gid, err := s.Consume(c2, nil); !errors.Is(err, ErrCodeReused) || gid != "g" {
		t.Fatalf("%v %q", err, gid)
	}
	if n := s.CancelForClient("y"); n != 0 {
		t.Fatalf("consumed request must not be cancelled: %d", n)
	}
}

func TestSweep(t *testing.T) {
	s, clk := newTestStore()
	mustCreate(t, s, "")
	r := mustCreate(t, s, "")
	code, _ := s.Approve(r.ID, Decision{ClientID: "c"})
	_, _, _ = s.Consume(code, func(Request) (string, error) { return "g", nil })
	clk.advance(RequestLifetime + TombstoneLifetime)
	s.Sweep()
	if n := s.size(); n != 0 {
		t.Fatalf("left %d entries", n)
	}
}
