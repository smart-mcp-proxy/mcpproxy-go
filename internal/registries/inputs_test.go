package registries

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectRequiredInputs_PlaceholderScan(t *testing.T) {
	entry := &ServerEntry{
		InstallCmd: "npx server --token ${GITHUB_TOKEN} --db $DATABASE_URL",
	}
	got := DetectRequiredInputs(entry)
	require.Len(t, got, 2)
	// Sorted, stable order.
	assert.Equal(t, "DATABASE_URL", got[0].Name)
	assert.Equal(t, "GITHUB_TOKEN", got[1].Name)
	// Token-ish names are flagged secret for masking.
	assert.True(t, got[1].Secret)
}

func TestDetectRequiredInputs_BraceDefaultStripped(t *testing.T) {
	entry := &ServerEntry{InstallCmd: "run ${API_KEY:-fallback}"}
	got := DetectRequiredInputs(entry)
	require.Len(t, got, 1)
	assert.Equal(t, "API_KEY", got[0].Name)
}

func TestDetectRequiredInputs_ExplicitMergedWithHeuristic(t *testing.T) {
	entry := &ServerEntry{
		InstallCmd:     "run ${GITHUB_TOKEN}",
		RequiredInputs: []RequiredInput{{Name: "GITHUB_TOKEN", Description: "GitHub PAT", Secret: true}},
	}
	got := DetectRequiredInputs(entry)
	require.Len(t, got, 1, "explicit + heuristic dup must collapse to one")
	assert.Equal(t, "GitHub PAT", got[0].Description, "explicit metadata preserved")
}

func TestDetectRequiredInputs_None(t *testing.T) {
	assert.Nil(t, DetectRequiredInputs(&ServerEntry{InstallCmd: "npx plain-server"}))
	assert.Nil(t, DetectRequiredInputs(nil))
}

func TestFindServerByIDIn(t *testing.T) {
	servers := []ServerEntry{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	got, err := findServerByIDIn(servers, "b")
	require.NoError(t, err)
	assert.Equal(t, "b", got.ID)

	_, err = findServerByIDIn(servers, "missing")
	assert.ErrorIs(t, err, ErrServerNotFound)

	_, err = findServerByIDIn(nil, "a")
	assert.ErrorIs(t, err, ErrServerNotFound)
}
