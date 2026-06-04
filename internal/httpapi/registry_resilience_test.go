package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
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

// provenanceController returns registries with provenance/trust data.
type provenanceController struct {
	*MockServerController
}

func (c *provenanceController) ListRegistries() ([]interface{}, error) {
	return []interface{}{
		map[string]interface{}{
			"id":         "official-reg",
			"name":       "Official Registry",
			"url":        "https://registry.example/official",
			"provenance": "official/trusted",
		},
		map[string]interface{}{
			"id":         "custom-reg",
			"name":       "Custom Registry",
			"url":        "https://registry.example/custom",
			"provenance": "custom/unverified",
		},
		map[string]interface{}{
			"id":   "no-provenance-reg",
			"name": "No Provenance Registry",
			"url":  "https://registry.example/none",
		},
	}, nil
}

// MCP-866: provenance is surfaced on the REST API; custom registries show
// provenance=custom/unverified and trusted=false.
func TestListRegistries_SurfacesProvenanceAndTrusted(t *testing.T) {
	srv := NewServer(&provenanceController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registries", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp contracts.GetRegistriesResponse
	decodeData(t, w, &resp)
	if resp.Total != 3 {
		t.Fatalf("expected 3 registries, got %d", resp.Total)
	}

	// Find each registry by ID and check provenance/trusted.
	byID := make(map[string]contracts.Registry, len(resp.Registries))
	for _, r := range resp.Registries {
		byID[r.ID] = r
	}

	// Official registry: provenance=official/trusted, trusted=true
	official, ok := byID["official-reg"]
	if !ok {
		t.Fatal("official-reg not found in response")
	}
	if official.Provenance != "official/trusted" {
		t.Errorf("official-reg provenance: want official/trusted, got %q", official.Provenance)
	}
	if !official.Trusted {
		t.Error("official-reg trusted: want true, got false")
	}

	// Custom registry: provenance=custom/unverified, trusted=false
	custom, ok := byID["custom-reg"]
	if !ok {
		t.Fatal("custom-reg not found in response")
	}
	if custom.Provenance != "custom/unverified" {
		t.Errorf("custom-reg provenance: want custom/unverified, got %q", custom.Provenance)
	}
	if custom.Trusted {
		t.Error("custom-reg trusted: want false, got true")
	}

	// No-provenance registry: provenance empty, trusted=false
	none, ok := byID["no-provenance-reg"]
	if !ok {
		t.Fatal("no-provenance-reg not found in response")
	}
	if none.Provenance != "" {
		t.Errorf("no-provenance-reg provenance: want empty, got %q", none.Provenance)
	}
	if none.Trusted {
		t.Error("no-provenance-reg trusted: want false, got true")
	}
}

// removeController simulates the server-side remove-source op: it removes the
// custom "acme" registry, refuses the built-in "official", and reports
// registry_not_found for anything else (MCP-1057).
type removeController struct {
	*MockServerController
}

func (c *removeController) RemoveRegistrySourceRef(id string) (*config.RegistryEntry, *contracts.RegistryAddError, error) {
	switch id {
	case "acme":
		return &config.RegistryEntry{
			ID:         "acme",
			Name:       "Acme",
			URL:        "https://acme.example/",
			Provenance: config.RegistryProvenanceCustom,
		}, nil, nil
	case "official":
		rerr := &contracts.RegistryAddError{Code: "registry_shadows_builtin", Message: `"official" is a built-in registry and cannot be removed`}
		return nil, rerr, errors.New(rerr.Message)
	default:
		rerr := &contracts.RegistryAddError{Code: "registry_not_found", Message: "no custom registry with id " + id}
		return nil, rerr, errors.New(rerr.Message)
	}
}

// MCP-1057: DELETE removes a custom registry and echoes it with trusted=false.
func TestRemoveRegistrySource_RemovesCustom(t *testing.T) {
	srv := NewServer(&removeController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/registries/acme", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp contracts.RemoveRegistrySourceData
	decodeData(t, w, &resp)
	if resp.Registry.ID != "acme" {
		t.Errorf("expected removed id=acme, got %q", resp.Registry.ID)
	}
	if resp.Registry.Trusted {
		t.Error("a custom registry must report trusted=false")
	}
}

// MCP-1057: removing a built-in is refused with 409 registry_shadows_builtin.
func TestRemoveRegistrySource_RefusesBuiltin(t *testing.T) {
	srv := NewServer(&removeController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/registries/official", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// MCP-1057: removing an unknown registry yields 404 registry_not_found.
func TestRemoveRegistrySource_NotFound(t *testing.T) {
	srv := NewServer(&removeController{&MockServerController{}}, zaptest.NewLogger(t).Sugar(), nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/registries/ghost", http.NoBody)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%s)", w.Code, w.Body.String())
	}
}
