package oauth

import (
	"strings"
	"testing"
	"time"
)

// SEC-01 second-lens review (PR #1350): httpLoggingMiddleware runs
// LogSafeRequestPath / LogSafeQueryString / LogSafeRequestURL on every
// request's r.URL.Path, r.URL.RawQuery and r.Referer(), mounted at the chi
// router root BEFORE apiKeyAuthMiddleware — so these renderers run on every
// unauthenticated connection the listener accepts, logged AFTER the response
// is already written and so not bounded by http.Server's ReadTimeout /
// WriteTimeout / ReadHeaderTimeout (those stop future reads/writes on the
// connection, not a goroutine already executing handler code).
//
// logSafeURLComponent splits its input on '/' and runs the shared value-shaped
// detector (Detector.MaskText: every built-in regex pattern plus a
// Shannon-entropy pass, with a fresh map allocated per pattern per call) once
// PER SEGMENT. A request path built from many minimal two-byte segments
// ("/a" repeated) fits comfortably inside the pre-existing 1MB MaxHeaderBytes
// budget and turns one request into hundreds of thousands of full detector
// passes — a large, unauthenticated CPU/allocation amplification introduced by
// the SEC-01 fix itself.
//
// These renderers must bound the cost of ANY single call independent of how
// long the caller-supplied string is: cut the input before running any
// redaction rule, so a request built from many small segments (or one huge
// query string, or a huge Referer) costs no more than a normal one.
func TestLogSafeRequestPath_BoundsCostOfAdversarialManySegmentPath(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skipped under -short")
	}

	// ~500,000 two-byte segments, matching the amplification the review
	// described (well inside the 1MB header budget the request line shares).
	adversarial := strings.Repeat("/a", 500_000)

	start := time.Now()
	_ = LogSafeRequestPath(adversarial)
	elapsed := time.Since(start)

	// Bounded by the input cap, not by the number of segments: this must stay
	// fast (low tens of milliseconds) regardless of how many segments the
	// client sent. A generous ceiling avoids flaking under CI load while still
	// catching the unbounded-per-segment behavior, which took long enough to
	// be a usable denial-of-service on its own.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("LogSafeRequestPath took %s on a %d-byte adversarial path — cost must be bounded, not O(segments)", elapsed, len(adversarial))
	}
}

func TestLogSafeQueryString_BoundsCostOfHugeQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skipped under -short")
	}

	adversarial := strings.Repeat("a", 2_000_000)

	start := time.Now()
	_ = LogSafeQueryString(adversarial)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("LogSafeQueryString took %s on a %d-byte adversarial query — cost must be bounded", elapsed, len(adversarial))
	}
}

func TestLogSafeRequestURL_BoundsCostOfHugeReferer(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skipped under -short")
	}

	adversarial := "http://host" + strings.Repeat("/a", 500_000)

	start := time.Now()
	_ = LogSafeRequestURL(adversarial)
	elapsed := time.Since(start)

	if elapsed > 500*time.Millisecond {
		t.Fatalf("LogSafeRequestURL took %s on a %d-byte adversarial referer — cost must be bounded", elapsed, len(adversarial))
	}
}

// A credential near the START of an oversized input — well within what any
// real access log line needs — must still be masked. The cap trades
// completeness on pathological input for bounded cost; it must not silently
// stop working on realistic ones.
func TestLogSafeRequestPath_StillMasksCredentialsWithinTheCap(t *testing.T) {
	const adminKey = "6930184070f362383e7cddbd1184b3b8ed66f84717eedd8906ab8743dfa746cd"

	path := "/ui/apikey=" + adminKey + strings.Repeat("/a", 10)
	got := LogSafeRequestPath(path)
	if strings.Contains(got, adminKey) {
		t.Fatalf("credential within the cap survived: %q", got)
	}
}

func TestLogSafeQueryString_StillMasksCredentialsWithinTheCap(t *testing.T) {
	const secret = "SUPERSECRETKEY0123456789abcdef01"

	got := LogSafeQueryString("apikey=" + secret + "&" + strings.Repeat("a", 10))
	if strings.Contains(got, secret) {
		t.Fatalf("credential within the cap survived: %q", got)
	}
}
