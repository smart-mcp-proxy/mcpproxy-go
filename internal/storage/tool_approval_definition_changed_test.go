package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

func TestSaveToolApproval_StampsDefinitionChangedAtOnContentChange(t *testing.T) {
	manager, cleanup := setupTestStorageForToolApproval(t)
	defer cleanup()

	base := func() *ToolApprovalRecord {
		return &ToolApprovalRecord{
			ServerName:          "srv",
			ToolName:            "notes",
			Status:              ToolApprovalStatusApproved,
			CurrentHash:         "h1",
			ApprovedHash:        "h1",
			CurrentDescription:  "reads notes",
			CurrentSchema:       `{"type":"object"}`,
			CurrentOutputSchema: `{"type":"string"}`,
		}
	}
	load := func() *ToolApprovalRecord {
		t.Helper()
		got, err := manager.GetToolApproval("srv", "notes")
		require.NoError(t, err)
		require.NotNil(t, got)
		return got
	}

	// (a) a brand-new record is never stamped.
	require.NoError(t, manager.SaveToolApproval(base()))
	assert.True(t, load().DefinitionChangedAt.IsZero(), "first capture must not be stamped")

	// (b) a status/disabled-only change carries the prior (zero) value.
	r := base()
	r.Disabled = true
	require.NoError(t, manager.SaveToolApproval(r))
	assert.True(t, load().DefinitionChangedAt.IsZero())

	// (c) each content field change stamps the stored record and the caller's pointer.
	mutations := []struct {
		name   string
		mutate func(*ToolApprovalRecord)
	}{
		{"description", func(r *ToolApprovalRecord) { r.CurrentDescription += " and sends them away" }},
		{"schema", func(r *ToolApprovalRecord) { r.CurrentSchema += " " }},
		{"output schema", func(r *ToolApprovalRecord) { r.CurrentOutputSchema = `{"type":"number"}` }},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			before := time.Now().Add(-time.Second)
			rec := load()
			rec.DefinitionChangedAt = time.Time{}
			m.mutate(rec)
			require.NoError(t, manager.SaveToolApproval(rec))
			stored := load()
			assert.True(t, stored.DefinitionChangedAt.After(before), "stored stamp should be about now")
			assert.True(t, rec.DefinitionChangedAt.Equal(stored.DefinitionChangedAt), "caller pointer carries the stamp")
		})
	}

	// (d) an annotations-only change leaves the stamp untouched.
	stamped := load()
	require.False(t, stamped.DefinitionChangedAt.IsZero())
	rec := load()
	rec.CurrentAnnotations = &config.ToolAnnotations{Title: "x"}
	require.NoError(t, manager.SaveToolApproval(rec))
	assert.True(t, load().DefinitionChangedAt.Equal(stamped.DefinitionChangedAt))

	// A caller that holds a stale copy without the stamp does not erase it.
	stale := load()
	stale.DefinitionChangedAt = time.Time{}
	stale.Status = ToolApprovalStatusPending
	require.NoError(t, manager.SaveToolApproval(stale))
	assert.True(t, load().DefinitionChangedAt.Equal(stamped.DefinitionChangedAt))

	// (e) SaveIntegrityBaselineWithBlocks preserves it.
	require.NoError(t, manager.db.SaveIntegrityBaselineWithBlocks(
		&scanner.IntegrityBaseline{ServerName: "srv"},
		[]scanner.ToolApprovalBlock{{ToolName: "notes", ApprovedAt: time.Now(), ApprovedBy: "test"}},
	))
	after := load()
	assert.True(t, after.Disabled)
	assert.True(t, after.DefinitionChangedAt.Equal(stamped.DefinitionChangedAt))
}
