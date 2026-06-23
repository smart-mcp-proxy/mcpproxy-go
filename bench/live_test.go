package bench

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubProxy returns an httptest server that mimics the two mcpproxy REST
// endpoints the live benchmark uses, wrapping payloads in the standard
// {success, data} envelope.
func stubProxy(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tools", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"tools": []map[string]any{
					{
						"name":        "read_text_file",
						"server_name": "filesystem",
						"description": "Read a file as text",
						"schema": map[string]any{
							"type":       "object",
							"properties": map[string]any{"path": map[string]any{"type": "string"}},
							"required":   []string{"path"},
						},
					},
					{
						"name":        "echo",
						"server_name": "memory",
						"description": "Echo input",
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/v1/index/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "" {
			http.Error(w, "missing q", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"query": r.URL.Query().Get("q"),
				"results": []map[string]any{
					{"tool": map[string]any{"name": "read_text_file", "server_name": "filesystem"}, "score": 0.9},
					{"tool": map[string]any{"name": "echo", "server_name": "memory"}, "score": 0.1},
				},
				"total": 2,
				"took":  "0ms",
			},
		})
	})
	return httptest.NewServer(mux)
}

func TestLiveClientFetchUpstreamTools(t *testing.T) {
	srv := stubProxy(t)
	defer srv.Close()

	c := NewLiveClient(srv.URL, "test-key")
	tools, err := c.FetchUpstreamTools(context.Background())
	if err != nil {
		t.Fatalf("FetchUpstreamTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2", len(tools))
	}
	if tools[0].ToolID != "filesystem:read_text_file" {
		t.Errorf("ToolID = %q, want filesystem:read_text_file", tools[0].ToolID)
	}
	if len(tools[0].Schema) == 0 {
		t.Errorf("expected schema captured for tool with input schema, got none")
	}
	if len(tools[1].Schema) != 0 {
		t.Errorf("expected no schema for schemaless tool, got %s", tools[1].Schema)
	}
}

func TestLiveClientSearch(t *testing.T) {
	srv := stubProxy(t)
	defer srv.Close()

	c := NewLiveClient(srv.URL, "test-key")
	ranked, latency, err := c.Search(context.Background(), "read a file", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []string{"filesystem:read_text_file", "memory:echo"}
	if len(ranked) != len(want) {
		t.Fatalf("ranked = %v, want %v", ranked, want)
	}
	for i := range want {
		if ranked[i] != want[i] {
			t.Errorf("ranked[%d] = %q, want %q", i, ranked[i], want[i])
		}
	}
	if latency < 0 {
		t.Errorf("latency should be non-negative, got %v", latency)
	}
}

func TestSchemaAwareTokenCountExceedsDescOnly(t *testing.T) {
	tk, err := NewTokenizer(DefaultEncoding)
	if err != nil {
		t.Fatalf("tokenizer: %v", err)
	}
	withSchema := Tool{
		Name:        "read_text_file",
		Description: "Read a file as text",
		Schema:      json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
	}
	descOnly := Tool{Name: withSchema.Name, Description: withSchema.Description}
	if tk.CountToolWithSchema(withSchema) <= tk.CountTool(descOnly) {
		t.Errorf("schema-aware count (%d) must exceed desc-only count (%d)",
			tk.CountToolWithSchema(withSchema), tk.CountTool(descOnly))
	}
	// A schemaless tool must count identically under both methods.
	if tk.CountToolWithSchema(descOnly) != tk.CountTool(descOnly) {
		t.Errorf("schemaless tool should count identically: %d vs %d",
			tk.CountToolWithSchema(descOnly), tk.CountTool(descOnly))
	}
}
