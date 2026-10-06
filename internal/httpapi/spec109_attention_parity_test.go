package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
)

// Spec 109-m SC-002 (T145, M6): REST and SSE legs of the attention parity
// chain. The Go fixture internal/runtime/testdata/attention_parity_fixture.json
// is computed by the real Compute, served by the real GET /attention handler,
// and the served `data` is written as the golden
// internal/runtime/testdata/attention_parity_rest.json that the CLI, vitest and
// XCTest replays render. UPDATE_GOLDEN=1 rewrites it.

const (
	p109AttentionFixture = "internal/runtime/testdata/attention_parity_fixture.json"
	p109AttentionGolden  = "internal/runtime/testdata/attention_parity_rest.json"
)

type p109AttentionFixtureFile struct {
	Now         time.Time                      `json:"now"`
	Input       internalRuntime.AttentionInput `json:"input"`
	ExpectedIDs []string                       `json:"expected_ids"`
}

type p109AttentionData struct {
	Count int                       `json:"count"`
	Items []contracts.AttentionItem `json:"items"`
}

func p109LoadAttentionFixture(t testing.TB) p109AttentionFixtureFile {
	t.Helper()
	var f p109AttentionFixtureFile
	p108ReadJSON(t, p109AttentionFixture, &f)
	f.Input.Now = f.Now
	return f
}

func p109AttentionIDs(items []contracts.AttentionItem) []string {
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// TestAttentionParity_RESTServesTheFixtureAndWritesTheGolden is the REST leg.
func TestAttentionParity_RESTServesTheFixtureAndWritesTheGolden(t *testing.T) {
	f := p109LoadAttentionFixture(t)
	items := internalRuntime.Compute(f.Input)
	require.Equal(t, f.ExpectedIDs, p109AttentionIDs(items), "Compute must give the fixture's expected ids")

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl.attentionItemsOverride = items
	srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})

	rec := scopeGet(t, srv, "/api/v1/attention", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	var body struct {
		Data p109AttentionData `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	assert.Equal(t, f.ExpectedIDs, p109AttentionIDs(body.Data.Items), "REST item order")
	assert.Equal(t, len(f.ExpectedIDs), body.Data.Count, "REST count equals the number of ids")

	golden := filepath.Join(p109Root(t), p109AttentionGolden)
	out := p109MarshalGolden(t, body.Data)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(golden, out, 0o644))
		return
	}
	committed, err := os.ReadFile(golden)
	require.NoError(t, err, "missing golden; run with UPDATE_GOLDEN=1")
	assert.JSONEq(t, string(out), string(committed), "attention_parity_rest.json is stale: UPDATE_GOLDEN=1 regenerates it")
}

// TestAttentionParity_SSECarriesTheSameIDs is the SSE leg: the attention.changed
// frame an admin subscriber gets names the same ids and the same count.
func TestAttentionParity_SSECarriesTheSameIDs(t *testing.T) {
	f := p109LoadAttentionFixture(t)
	items := internalRuntime.Compute(f.Input)

	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, _ := scopedAgentServer(t, ctrl, []string{"alpha"})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	body, closeFn := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer closeFn()
	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 1 }, 5*time.Second, 20*time.Millisecond)

	done := make(chan sseEvent, 1)
	deadline := time.Now().Add(10 * time.Second)
	go func() { done <- readSSEUntil(t, body, "attention.changed", deadline) }()
	ctrl.publishToAll(internalRuntime.Event{
		Type:      internalRuntime.EventTypeAttentionChanged,
		Payload:   map[string]any{"count": len(items), "items": internalRuntime.AttentionEventItems(items)},
		Timestamp: time.Now(),
	})
	evt := <-done

	payload := evt.Data["payload"].(map[string]interface{})
	var ids []string
	for _, id := range payload["ids"].([]interface{}) {
		ids = append(ids, id.(string))
	}
	assert.Equal(t, f.ExpectedIDs, ids, "SSE id order")
	assert.Equal(t, float64(len(f.ExpectedIDs)), payload["count"])
}
