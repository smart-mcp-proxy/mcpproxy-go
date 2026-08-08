package httpapi

import (
	"math"
	"time"
)

// defaultRetryAfterSeconds is the hint used when the shedding scope reported no
// queue_timeout (a `queue_size: 0` scope sheds instantly and has no wait
// budget). Something small and concrete beats omitting the header: a client
// with no hint typically retries immediately and re-sheds.
const defaultRetryAfterSeconds = 1

// retryAfterSeconds converts a scope's effective queue_timeout into the
// Retry-After header value (RFC 9110 delta-seconds, spec 093 FR-011). Sub-second
// budgets round UP to 1 so the header never says "retry now".
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return defaultRetryAfterSeconds
	}
	secs := int(math.Ceil(d.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}
