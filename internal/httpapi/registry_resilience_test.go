package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/registries"
	"go.uber.org/zap/zaptest"
)

// keyMissingController surfaces ErrRegistryKeyMissing from a registry search.
type keyMissingController struct {
	*MockServerController
}

func (c *keyMissingController) SearchRegistryServers(_, _, _ string, _ int) ([]interface{}, *contracts.RegistryCacheInfo, error) {
	return nil, nil, registries.ErrRegistryKeyMissing
}

// cachedController surfaces a freshness indicator alongside results.
type cachedController struct {
	*MockServerController
}

func (c *cachedController) SearchRegistryServers(_, _, _ string, _ int) ([]interface{}, *contracts.RegistryCacheInfo, error) {
	return []interface{}{}, &contracts.RegistryCacheInfo{AgeSeconds: 42, Stale: true}, nil
}

// refreshCountController reports a fixed number of cleared cache entries.
type refreshCountController struct {
	*MockServerController
}

func (c *refreshCountController) RefreshRegistryCache(_ string) (int, error) { return 3, nil }

func decodeData(t *testing.T, w *httptest.ResponseRecorder, into interface{}) {
	t.Helper()
	var env struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body=%s)", err, w.Body.String())
	}
	if !env.Success {
		t.Fatalf("expected success envelope, got: %s", w.Body.String())
	}
	if err := json.Unmarshal(env.Data, into); err != nil {
		t.Fatalf("decode data: %v", err)
	}
}

// FR-008: a key-absent registry yields 200 with an unavailable marker, not 500.
func TestSearchRegistryServers_KeyMissingIsUnavailableNot500(t *testing.T) {
	srv := NewServer(&keyMissingController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registries/needs-key/servers", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp contracts.SearchRegistryServersResponse
	decodeData(t, w, &resp)
	if resp.Unavailable == nil || resp.Unavailable.Reason == "" {
		t.Errorf("expected unavailable marker with reason, got %+v", resp.Unavailable)
	}
	if resp.Total != 0 {
		t.Errorf("expected 0 servers, got %d", resp.Total)
	}
}

// FR-007: cache freshness is surfaced on the search response.
func TestSearchRegistryServers_CacheFreshnessSurfaced(t *testing.T) {
	srv := NewServer(&cachedController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registries/pulse/servers", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var resp contracts.SearchRegistryServersResponse
	decodeData(t, w, &resp)
	if resp.Cache == nil {
		t.Fatal("expected cache freshness block")
	}
	if resp.Cache.AgeSeconds != 42 || !resp.Cache.Stale {
		t.Errorf("cache info not surfaced verbatim: %+v", resp.Cache)
	}
}

// FR-007: the refresh endpoint reports how many cache entries were dropped.
func TestRefreshRegistryCache_Endpoint(t *testing.T) {
	srv := NewServer(&refreshCountController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registries/pulse/refresh", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp contracts.RefreshRegistryResponse
	decodeData(t, w, &resp)
	if resp.Cleared != 3 {
		t.Errorf("expected cleared=3, got %d", resp.Cleared)
	}
	if resp.RegistryID != "pulse" {
		t.Errorf("expected registry_id=pulse, got %q", resp.RegistryID)
	}
}
