package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 117 PR-2 T201: the review fingerprint.

func fpBool(b bool) *bool { return &b }

func fpRecord() *storage.ToolApprovalRecord {
	return &storage.ToolApprovalRecord{
		ServerName:          "notes2",
		ToolName:            "read_note",
		CurrentHash:         "abc123",
		CurrentDescription:  "Read a note",
		CurrentSchema:       `{"type":"object"}`,
		CurrentOutputSchema: "",
		CurrentAnnotations:  &config.ToolAnnotations{ReadOnlyHint: fpBool(true)},
		Status:              storage.ToolApprovalStatusPending,
	}
}

var fingerprintShape = regexp.MustCompile(`^rv2-[0-9a-f]{64}$`)

func TestReviewFingerprint_FormatAndStability(t *testing.T) {
	fp := reviewFingerprint(fpRecord())
	assert.Regexp(t, fingerprintShape, fp)
	assert.Equal(t, fp, reviewFingerprint(fpRecord()), "deterministic")
}

func TestReviewFingerprint_ChangesWithDefinitionAndHints(t *testing.T) {
	base := reviewFingerprint(fpRecord())
	mutations := map[string]func(r *storage.ToolApprovalRecord){
		"hash (description/schemas)": func(r *storage.ToolApprovalRecord) { r.CurrentHash = "def456" },
		"tool name":                  func(r *storage.ToolApprovalRecord) { r.ToolName = "read_note2" },
		"server name":                func(r *storage.ToolApprovalRecord) { r.ServerName = "notes3" },
		"readOnly true->false":       func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.ReadOnlyHint = fpBool(false) },
		"readOnly true->nil":         func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.ReadOnlyHint = nil },
		"destructive nil->false":     func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.DestructiveHint = fpBool(false) },
		"destructive nil->true":      func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.DestructiveHint = fpBool(true) },
		"idempotent nil->true":       func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.IdempotentHint = fpBool(true) },
		"openWorld nil->false":       func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.OpenWorldHint = fpBool(false) },
	}
	seen := map[string]string{base: "base"}
	for name, mutate := range mutations {
		rec := fpRecord()
		mutate(rec)
		fp := reviewFingerprint(rec)
		assert.NotEqual(t, base, fp, "%s must change the fingerprint", name)
		if prev, dup := seen[fp]; dup {
			t.Errorf("%s collides with %s", name, prev)
		}
		seen[fp] = name
	}
}

func TestReviewFingerprint_IgnoresNonContractFields(t *testing.T) {
	base := reviewFingerprint(fpRecord())
	for name, mutate := range map[string]func(r *storage.ToolApprovalRecord){
		"title":      func(r *storage.ToolApprovalRecord) { r.CurrentAnnotations.Title = "A title" },
		"status":     func(r *storage.ToolApprovalRecord) { r.Status = storage.ToolApprovalStatusApproved },
		"disabled":   func(r *storage.ToolApprovalRecord) { r.Disabled = true },
		"timestamps": func(r *storage.ToolApprovalRecord) { r.ApprovedAt = time.Now(); r.ApprovedBy = "x" },
		"held evidence": func(r *storage.ToolApprovalRecord) {
			r.HeldReason = storage.ToolHeldReasonScanFindings
			r.HeldSignals = []string{"tpa.x"}
		},
		"approved hash / previous": func(r *storage.ToolApprovalRecord) {
			r.ApprovedHash = "zzz"
			r.PreviousDescription = "old"
		},
	} {
		rec := fpRecord()
		mutate(rec)
		assert.Equal(t, base, reviewFingerprint(rec), "%s must not change the fingerprint", name)
	}
}

func TestReviewFingerprint_TwoServersSameDefinitionDiffer(t *testing.T) {
	a, b := fpRecord(), fpRecord()
	b.ServerName = "other"
	assert.NotEqual(t, reviewFingerprint(a), reviewFingerprint(b))
}

func TestReviewFingerprint_EmptyCurrentHashIsDefinitionSensitive(t *testing.T) {
	rec := fpRecord()
	rec.CurrentHash = ""
	fp := reviewFingerprint(rec)
	assert.Regexp(t, fingerprintShape, fp, "non-empty for a hashless record (FR-003)")
	rec2 := fpRecord()
	rec2.CurrentHash = ""
	rec2.CurrentDescription = "Delete every note"
	assert.NotEqual(t, fp, reviewFingerprint(rec2))
	rec3 := fpRecord()
	rec3.CurrentHash = ""
	rec3.CurrentSchema = `{"type":"object","properties":{"x":{}}}`
	assert.NotEqual(t, fp, reviewFingerprint(rec3))
}

// fingerprintVector pins the byte format across releases
// (testdata/review_fingerprint_vectors.json). Regenerate only with a new
// version prefix: MCPPROXY_UPDATE_FINGERPRINT_VECTORS=1.
type fingerprintVector struct {
	Name   string                      `json:"name"`
	Record *storage.ToolApprovalRecord `json:"record"`
	Want   string                      `json:"fingerprint"`
}

func TestReviewFingerprint_GoldenVectors(t *testing.T) {
	path := filepath.Join("testdata", "review_fingerprint_vectors.json")
	if os.Getenv("MCPPROXY_UPDATE_FINGERPRINT_VECTORS") == "1" {
		hashless := fpRecord()
		hashless.CurrentHash = ""
		vectors := []fingerprintVector{
			{Name: "read-only", Record: fpRecord()},
			{Name: "no annotations", Record: &storage.ToolApprovalRecord{ServerName: "s", ToolName: "t", CurrentHash: "h"}},
			{Name: "destructive", Record: &storage.ToolApprovalRecord{ServerName: "s", ToolName: "ns:erase", CurrentHash: "h2",
				CurrentAnnotations: &config.ToolAnnotations{ReadOnlyHint: fpBool(false), DestructiveHint: fpBool(true), IdempotentHint: fpBool(false), OpenWorldHint: fpBool(true)}}},
			{Name: "hashless", Record: hashless},
		}
		for i := range vectors {
			vectors[i].Want = reviewFingerprint(vectors[i].Record)
		}
		data, err := json.MarshalIndent(vectors, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(data, '\n'), 0o600))
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var vectors []fingerprintVector
	require.NoError(t, json.Unmarshal(data, &vectors))
	require.NotEmpty(t, vectors)
	for _, v := range vectors {
		assert.Equal(t, v.Want, reviewFingerprint(v.Record), v.Name)
	}
}
