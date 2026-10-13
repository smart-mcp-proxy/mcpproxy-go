package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 109-m SC-002 (T145, M6): the shared attention fixture. This test is the
// Go root of the replay chain: Compute over the fixture gives the ids every
// surface must show, in order. internal/httpapi/spec109_attention_parity_test.go
// serves the same items through GET /attention and writes the golden the CLI,
// vitest and XCTest replays render.

type attentionParityFixture struct {
	Now         time.Time      `json:"now"`
	Input       AttentionInput `json:"input"`
	ExpectedIDs []string       `json:"expected_ids"`
}

func loadAttentionParityFixture(t testing.TB) attentionParityFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "attention_parity_fixture.json"))
	require.NoError(t, err)
	var f attentionParityFixture
	require.NoError(t, json.Unmarshal(raw, &f))
	f.Input.Now = f.Now
	return f
}

func TestAttentionParity_ComputeGivesTheExpectedIDsInOrder(t *testing.T) {
	f := loadAttentionParityFixture(t)
	items := Compute(f.Input)

	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	assert.Equal(t, f.ExpectedIDs, ids)

	// The disabled server contributes nothing, and a quarantined OAuth server
	// yields both a sign-in item and a review item (the US1 independent test).
	assert.NotContains(t, ids, "server_error:server:parked")
	assert.Contains(t, ids, "sign_in_required:server:github-oauth")
	assert.Contains(t, ids, "server_review:server:github-oauth")
}

func TestAttentionParity_FixtureIsStable(t *testing.T) {
	f := loadAttentionParityFixture(t)
	first := Compute(f.Input)
	second := Compute(f.Input)
	assert.Equal(t, first, second, "Compute is pure: the same snapshot gives the same list")
}
