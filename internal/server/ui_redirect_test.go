package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUIRedirectTarget(t *testing.T) {
	tests := []struct {
		name   string
		method string
		url    string
		want   string
		ok     bool
	}{
		{
			name:   "bare UI route",
			method: http.MethodGet,
			url:    "/activity",
			want:   "/ui/activity",
			ok:     true,
		},
		{
			name:   "keeps the api key",
			method: http.MethodGet,
			url:    "/activity?apikey=abc123",
			want:   "/ui/activity?apikey=abc123",
			ok:     true,
		},
		{
			name:   "nested route",
			method: http.MethodGet,
			url:    "/security/scans/scan-everything-1",
			want:   "/ui/security/scans/scan-everything-1",
			ok:     true,
		},
		{
			name:   "encoded segment stays encoded",
			method: http.MethodGet,
			url:    "/servers/my%2Fserver",
			want:   "/ui/servers/my%2Fserver",
			ok:     true,
		},
		{
			name:   "unknown path still 404s",
			method: http.MethodGet,
			url:    "/not-a-ui-route",
			ok:     false,
		},
		{
			name:   "api paths are never redirected",
			method: http.MethodGet,
			url:    "/apiv1/servers",
			ok:     false,
		},
		{
			name:   "protocol-relative path refused",
			method: http.MethodGet,
			url:    "//evil.example/servers",
			ok:     false,
		},
		{
			name:   "root is handled elsewhere",
			method: http.MethodGet,
			url:    "/",
			ok:     false,
		},
		{
			name:   "non-GET is not a deep link",
			method: http.MethodPost,
			url:    "/activity",
			ok:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://127.0.0.1:8080"+tc.url, nil)
			got, ok := uiRedirectTarget(req)
			if ok != tc.ok {
				t.Fatalf("uiRedirectTarget(%q) ok = %v, want %v (target %q)", tc.url, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Fatalf("uiRedirectTarget(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
