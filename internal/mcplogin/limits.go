package mcplogin

import (
	"math"
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

// Limiter rate limits the public OAuth endpoints per endpoint and remote
// address. It is safe for concurrent use.
type Limiter struct {
	rates *limiter.TokenRates
}

// NewLimiter returns a Limiter with the default rates. now is the clock; nil
// means time.Now.
func NewLimiter(now func() time.Time) *Limiter {
	return &Limiter{rates: limiter.NewTokenRates(now)}
}

// Allow charges one call of endpoint from remoteIP. When refused,
// retryAfterSeconds is at least 1. An unknown endpoint is not limited.
func (l *Limiter) Allow(endpoint, remoteIP string) (ok bool, retryAfterSeconds int) {
	spec, known := rateFor(endpoint)
	if !known {
		return true, 0
	}
	d := l.rates.Allow(endpoint+"|"+remoteIP, &spec)
	if d.Allowed {
		return true, 0
	}
	return false, max(1, int(math.Ceil(d.RetryAfter.Seconds())))
}
