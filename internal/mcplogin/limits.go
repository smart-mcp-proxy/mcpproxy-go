package mcplogin

import (
	"math"
	"sync"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

const (
	// MaxBodyBytes is the largest request body register and token accept.
	MaxBodyBytes = 16 << 10
	// MaxPendingRegistrations caps registrations that are not yet approved.
	MaxPendingRegistrations = 50
	// MaxPendingRequests caps pending authorization requests.
	MaxPendingRequests = 20
)

// Endpoint names the rate-limited public endpoints.
const (
	// EndpointRegister is POST /oauth/register.
	EndpointRegister = "register"
	// EndpointAuthorize is GET /oauth/authorize.
	EndpointAuthorize = "authorize"
	// EndpointToken is POST /oauth/token.
	EndpointToken = "token"
)

// rateFor returns the default budget of an endpoint.
func rateFor(endpoint string) (limiter.RateSpec, bool) {
	switch endpoint {
	case EndpointRegister:
		return limiter.RateSpec{PerMinute: 10, Burst: 5}, true
	case EndpointAuthorize:
		return limiter.RateSpec{PerMinute: 30, Burst: 10}, true
	case EndpointToken:
		return limiter.RateSpec{PerMinute: 60, Burst: 20}, true
	}
	return limiter.RateSpec{}, false
}

const (
	// MaxTrackedKeys caps the distinct endpoint|address keys the Limiter keeps.
	MaxTrackedKeys = 4096
	// idleAfter is how long a key must be unseen before it may be dropped. It
	// exceeds the time any bucket needs to refill completely, so dropping an
	// idle key never hands a caller extra budget.
	idleAfter = time.Minute
)

// Limiter rate limits the public OAuth endpoints per endpoint and remote
// address, over at most MaxTrackedKeys keys. It is safe for concurrent use.
type Limiter struct {
	rates *limiter.TokenRates
	now   func() time.Time
	mu    sync.Mutex
	seen  map[string]time.Time
}

// NewLimiter returns a Limiter with the default rates. now is the clock; nil
// means time.Now.
func NewLimiter(now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{rates: limiter.NewTokenRates(now), now: now, seen: map[string]time.Time{}}
}

// admit records key as seen now. For an unseen key at the cap it prunes idle
// keys first and, if the limiter is still full, refuses with the seconds until
// the oldest key becomes idle. Active keys are never evicted.
func (l *Limiter) admit(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if _, known := l.seen[key]; !known && len(l.seen) >= MaxTrackedKeys {
		oldest := now
		for k, t := range l.seen {
			if now.Sub(t) >= idleAfter {
				delete(l.seen, k)
			} else if t.Before(oldest) {
				oldest = t
			}
		}
		if len(l.seen) >= MaxTrackedKeys {
			wait := idleAfter - now.Sub(oldest)
			return false, max(1, int(math.Ceil(wait.Seconds())))
		}
	}
	l.seen[key] = now
	return true, 0
}

// Allow charges one call of endpoint from remoteIP. When refused,
// retryAfterSeconds is at least 1. An unknown endpoint is not limited. A new
// address is refused while the limiter is full of active ones.
func (l *Limiter) Allow(endpoint, remoteIP string) (ok bool, retryAfterSeconds int) {
	spec, known := rateFor(endpoint)
	if !known {
		return true, 0
	}
	key := endpoint + "|" + remoteIP
	if ok, retry := l.admit(key); !ok {
		return false, retry
	}
	d := l.rates.Allow(key, &spec)
	if d.Allowed {
		return true, 0
	}
	return false, max(1, int(math.Ceil(d.RetryAfter.Seconds())))
}
