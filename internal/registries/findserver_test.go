package registries

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFindServerByID_BeyondSearchLimit reproduces Codex RV #1: a server that
// sits past the first page / UI limit of a registry listing must still be
// addable via FindServerByID, not merely searchable. The official listing here
// returns 60 entries on a single page with the target at index 55 — past the
// old 50-entry add cap that SearchServers applied before matching.
func TestFindServerByID_BeyondSearchLimit(t *testing.T) {
	const target = "io.example/target-server"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		items := make([]map[string]interface{}, 0, 60)
		for i := 0; i < 60; i++ {
			name := fmt.Sprintf("io.example/filler-%02d", i)
			if i == 55 {
				name = target
			}
			items = append(items, map[string]interface{}{
				"server": map[string]interface{}{
					"name":        name,
					"description": "test server",
					"remotes": []interface{}{
						map[string]interface{}{"type": "streamable-http", "url": "https://example.com/" + name},
					},
				},
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"servers":  items,
			"metadata": map[string]interface{}{}, // no nextCursor => single page
		})
	}))
	defer srv.Close()

	registryList = []RegistryEntry{
		{ID: "official", Name: "Official", ServersURL: srv.URL, Protocol: protocolOfficial},
	}

	got, err := FindServerByID(context.Background(), "official", target, nil)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, target, got.ID, "server beyond the UI/search limit must still be addable")
}

// TestFetchServers_SendsConfiguredAPIKey covers Codex RV #2 for the generic
// (non-official) request builder used by Pulse: a configured key must reach the
// wire as a Bearer token, not be read-but-ignored.
func TestFetchServers_SendsConfiguredAPIKey(t *testing.T) {
	const key = "pulse-secret-123"
	t.Setenv(RegistryKeyEnvVar("pulse"), key)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "pulse", Protocol: "custom/pulse", ServersURL: srv.URL, RequiresKey: true}
	_, err := fetchServers(context.Background(), reg, nil, "")
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+key, gotAuth, "configured registry key must be sent as a Bearer token")
}

// TestFetchOfficialServers_SendsConfiguredAPIKey covers Codex RV #2 for the
// official-protocol request builder used by Smithery.
func TestFetchOfficialServers_SendsConfiguredAPIKey(t *testing.T) {
	const key = "smithery-secret-456"
	t.Setenv(RegistryKeyEnvVar("smithery"), key)

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[],"metadata":{}}`))
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "smithery", Protocol: protocolOfficial, ServersURL: srv.URL, RequiresKey: true}
	_, err := fetchOfficialServers(context.Background(), reg, nil, "")
	require.NoError(t, err)
	assert.Equal(t, "Bearer "+key, gotAuth, "configured registry key must be sent as a Bearer token")
}

// TestFetchServers_NoKeyNoAuthHeader ensures no Authorization header is attached
// when no key is configured (never send a bare "Bearer ").
func TestFetchServers_NoKeyNoAuthHeader(t *testing.T) {
	t.Setenv(RegistryKeyEnvVar("pulse"), "")

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[]}`))
	}))
	defer srv.Close()

	reg := &RegistryEntry{ID: "pulse", Protocol: "custom/pulse", ServersURL: srv.URL}
	_, err := fetchServers(context.Background(), reg, nil, "")
	require.NoError(t, err)
	assert.Empty(t, gotAuth, "no Authorization header should be sent without a configured key")
}
