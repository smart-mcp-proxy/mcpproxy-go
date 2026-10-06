package transport

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/headerfwd"
)

func TestHTTPErrorScrubbedCopy(t *testing.T) {
	scrub := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[x]") }
	orig := &HTTPError{
		StatusCode: 500,
		Headers:    map[string]string{"X-Echo": "SECRET"},
		Body:       "echo SECRET",
		URL:        "https://up/mcp",
		Err:        errors.New("read SECRET"),
	}
	c, ok := orig.ScrubbedCopy(scrub).(*HTTPError)
	if !ok {
		t.Fatal("copy must be *HTTPError")
	}
	if strings.Contains(c.Error(), "SECRET") || strings.Contains(c.Headers["X-Echo"], "SECRET") ||
		strings.Contains(c.Err.Error(), "SECRET") {
		t.Fatalf("copy not scrubbed: %+v", c)
	}
	if orig.Body != "echo SECRET" || orig.Headers["X-Echo"] != "SECRET" {
		t.Fatal("receiver must not be modified")
	}
	if c.StatusCode != 500 || c.URL != orig.URL {
		t.Fatal("non-text fields must be preserved")
	}
}

func TestJSONRPCErrorScrubbedCopy(t *testing.T) {
	scrub := func(s string) string { return strings.ReplaceAll(s, "SECRET", "[x]") }
	orig := &JSONRPCError{
		Code:      -32602,
		Message:   "bad SECRET",
		Data:      map[string]interface{}{"echo": "SECRET", "n": 1},
		HTTPError: &HTTPError{StatusCode: 400, Body: "SECRET"},
	}
	c, ok := orig.ScrubbedCopy(scrub).(*JSONRPCError)
	if !ok {
		t.Fatal("copy must be *JSONRPCError")
	}
	if strings.Contains(c.Error(), "SECRET") {
		t.Fatalf("copy not scrubbed: %s", c.Error())
	}
	if d, _ := c.Data.(map[string]interface{}); d == nil || d["echo"] != "[x]" {
		t.Fatalf("data not scrubbed: %#v", c.Data)
	}
	if c.Code != -32602 || orig.Message != "bad SECRET" || orig.HTTPError.Body != "SECRET" {
		t.Fatal("code preserved, receiver untouched")
	}
}

// Review round 9: createDetailedErrorResponse pulls *HTTPError out with
// errors.As; through headerfwd.ScrubError it must get the scrubbed copy.
func TestScrubErrorAsReturnsScrubbedHTTPError(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("X-Tenant-Id", "tenant-secret-9")
	snap := headerfwd.Capture(r, map[string]struct{}{"X-Tenant-Id": {}})
	raw := &HTTPError{StatusCode: 502, Body: "upstream echoed tenant-secret-9"}
	err := headerfwd.ScrubError(fmt.Errorf("call: %w", raw), snap, []string{"X-Tenant-Id"})

	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatal("HTTPError must stay reachable")
	}
	if he == raw || strings.Contains(he.Body, "tenant-secret-9") {
		t.Fatalf("errors.As leaked the raw body: %q", he.Body)
	}
	if he.StatusCode != 502 {
		t.Fatal("status must be preserved")
	}
}
