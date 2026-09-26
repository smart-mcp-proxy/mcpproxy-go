package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_CatalogSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/catalog/search" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("q") != "github" || q.Get("source") != "official" || q.Get("tag") != "" || q.Get("limit") != "5" {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"query": "github",
				"results": [{"source":"official","id":"gh","title":"GitHub","transport":"http","install":{"url":"https://x"},"added":false}],
				"sections": null,
				"unavailable": []
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, nil)
	resp, err := c.CatalogSearch(context.Background(), "github", "official", "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Query != "github" {
		t.Errorf("unexpected query: %q", resp.Query)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != "gh" {
		t.Fatalf("unexpected results: %+v", resp.Results)
	}
}
