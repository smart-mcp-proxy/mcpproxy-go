package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

type createOnlyImportController struct {
	mockImportController
	existing []string
	addErr   map[string]error
}

func (m *createOnlyImportController) GetAllServers() ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(m.existing))
	for _, n := range m.existing {
		out = append(out, map[string]interface{}{"name": n})
	}
	return out, nil
}

func (m *createOnlyImportController) AddServer(_ context.Context, sc *config.ServerConfig) error {
	return m.addErr[sc.Name]
}

const createOnlyImportContent = `{"mcpServers":{"alpha":{"command":"a"},"beta":{"command":"b"}}}`

func runCreateOnlyImport(t *testing.T, c *createOnlyImportController, rename map[string]string) *ImportResponse {
	t.Helper()
	server := NewServer(c, zap.NewNop().Sugar(), nil)
	req := httptest.NewRequest("POST", "/api/v1/servers/import/path", http.NoBody)
	resp, err := server.runImport(req, []byte(createOnlyImportContent), "claude-desktop", nil, false, rename, nil, false)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	return resp
}

// A rename that lands on an already-configured name is reported as skipped
// already_exists, not claimed as imported.
func TestRunImport_RenameOntoExistingIsSkipped(t *testing.T) {
	c := &createOnlyImportController{existing: []string{"taken"}}
	resp := runCreateOnlyImport(t, c, map[string]string{"alpha": "taken"})
	if len(resp.Imported) != 1 || resp.Imported[0].Name != "beta" {
		t.Fatalf("imported = %+v, want only beta", resp.Imported)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].Name != "taken" || resp.Skipped[0].Reason != "already_exists" {
		t.Fatalf("skipped = %+v", resp.Skipped)
	}
}

// A post-add failure is reported in Failed and removed from Imported.
func TestRunImport_PostAddFailureIsReported(t *testing.T) {
	c := &createOnlyImportController{addErr: map[string]error{
		"alpha": errors.New("boom"),
		"beta":  errors.New("server 'beta' already exists"),
	}}
	resp := runCreateOnlyImport(t, c, nil)
	if len(resp.Imported) != 0 {
		t.Fatalf("imported = %+v, want none", resp.Imported)
	}
	if len(resp.Failed) != 1 || resp.Failed[0].Name != "alpha" || resp.Summary.Failed != 1 {
		t.Fatalf("failed = %+v summary=%+v", resp.Failed, resp.Summary)
	}
	if len(resp.Skipped) != 1 || resp.Skipped[0].Name != "beta" || resp.Summary.Imported != 0 {
		t.Fatalf("skipped = %+v summary=%+v", resp.Skipped, resp.Summary)
	}
}
