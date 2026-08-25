package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		value string
		want  time.Duration
		ok    bool
	}{
		{"empty", "", 0, false},
		{"seconds", "3600", time.Hour, true},
		{"seconds with padding", "  30 ", 30 * time.Second, true},
		{"zero seconds", "0", 0, false},
		{"negative seconds", "-5", 0, false},
		{"http date in future", now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{"http date in past", now.Add(-time.Minute).Format(http.TimeFormat), 0, false},
		{"malformed", "soon please", 0, false},
		{"float seconds are malformed per RFC 7231", "12.5", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRetryAfter(tt.value, now)
			if ok != tt.ok {
				t.Fatalf("ParseRetryAfter(%q) ok = %v, want %v", tt.value, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("ParseRetryAfter(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestRetryAfterRecorder_CapsAndKeepsFurthestDeadline(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	rec := NewRetryAfterRecorder()

	if !rec.Deadline().IsZero() {
		t.Fatalf("fresh recorder should have no deadline, got %v", rec.Deadline())
	}

	rec.Record(now, 30*time.Second, http.StatusTooManyRequests)
	if got, want := rec.Deadline(), now.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("deadline = %v, want %v", got, want)
	}

	// A shorter hint must not shorten an outstanding park window.
	rec.Record(now, 5*time.Second, http.StatusTooManyRequests)
	if got, want := rec.Deadline(), now.Add(30*time.Second); !got.Equal(want) {
		t.Fatalf("deadline after shorter hint = %v, want %v", got, want)
	}

	// Anything beyond the cap is clamped.
	rec.Record(now, 72*time.Hour, http.StatusTooManyRequests)
	if got, want := rec.Deadline(), now.Add(MaxRetryAfterDelay); !got.Equal(want) {
		t.Fatalf("deadline after oversized hint = %v, want %v", got, want)
	}

	rec.Clear()
	if !rec.Deadline().IsZero() {
		t.Fatalf("Clear should drop the deadline, got %v", rec.Deadline())
	}
}

func TestRetryAfterRecorder_NilSafe(t *testing.T) {
	var rec *RetryAfterRecorder
	rec.Record(time.Now(), time.Minute, http.StatusTooManyRequests)
	rec.Clear()
	if !rec.Deadline().IsZero() {
		t.Fatal("nil recorder must report a zero deadline")
	}
}

func TestRetryAfterTransport_RecordsRateLimitHints(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		header     string
		wantRecord bool
		wantDelay  time.Duration
	}{
		{"429 with delta-seconds", http.StatusTooManyRequests, "3600", true, time.Hour},
		{"429 with http-date", http.StatusTooManyRequests, "", true, 120 * time.Second}, // filled in below
		{"429 with malformed header", http.StatusTooManyRequests, "later", false, 0},
		{"429 with no header", http.StatusTooManyRequests, "", false, 0},
		{"503 with header", http.StatusServiceUnavailable, "45", true, 45 * time.Second},
		{"503 without header", http.StatusServiceUnavailable, "", false, 0},
		{"200 with header is ignored", http.StatusOK, "3600", false, 0},
		{"401 with header is ignored", http.StatusUnauthorized, "3600", false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := tt.header
			if tt.name == "429 with http-date" {
				header = time.Now().Add(120 * time.Second).UTC().Format(http.TimeFormat)
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if header != "" {
					w.Header().Set("Retry-After", header)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(`{"error":"slow down"}`))
			}))
			defer srv.Close()

			rec := NewRetryAfterRecorder()
			httpClient := &http.Client{
				Transport: NewRetryAfterTransport(http.DefaultTransport, rec, zap.NewNop()),
			}

			before := time.Now()
			resp, err := httpClient.Get(srv.URL)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d (the wrapper must pass the response through untouched)", resp.StatusCode, tt.status)
			}

			deadline := rec.Deadline()
			if !tt.wantRecord {
				if !deadline.IsZero() {
					t.Fatalf("expected no recorded hint, got deadline %v", deadline)
				}
				return
			}
			if deadline.IsZero() {
				t.Fatal("expected a recorded deadline, got none")
			}
			// Allow generous slack: HTTP-date has one-second granularity.
			gotDelay := deadline.Sub(before)
			if gotDelay < tt.wantDelay-2*time.Second || gotDelay > tt.wantDelay+2*time.Second {
				t.Fatalf("recorded delay = %v, want ~%v", gotDelay, tt.wantDelay)
			}
		})
	}
}

// TestRetryAfterTransport_BodyIsIntact guards the one thing a response-inspecting
// RoundTripper can easily break: the body must still be readable downstream.
func TestRetryAfterTransport_BodyIsIntact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	rec := NewRetryAfterRecorder()
	httpClient := &http.Client{Transport: NewRetryAfterTransport(nil, rec, nil)}

	resp, err := httpClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "rate limited" {
		t.Fatalf("body = %q, want %q", string(buf[:n]), "rate limited")
	}
	if rec.Deadline().IsZero() {
		t.Fatal("expected the 429 hint to be recorded")
	}
}
