package registries

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The compiled-in reference catalog gives every entry an id that already
// starts with its source ("reference/time"), and `catalog search` prints that
// id in its ID column. A user who copies it writes `catalog add
// reference/time`, which splits into source "reference" and id "time". Before
// this fix only the doubled ref "reference/reference/time" resolved, so the
// first-run catalog door failed with "server not found in registry".
func TestFindServerByID_AcceptsSourcePrefixedReferenceID(t *testing.T) {
	registryList = []RegistryEntry{{ID: "reference", Name: "Reference", Protocol: protocolReference}}

	for _, id := range []string{"time", "reference/time"} {
		got, err := FindServerByID(context.Background(), "reference", id, nil)
		require.NoError(t, err, "id %q must resolve", id)
		assert.Equal(t, "reference/time", got.ID)
	}

	_, err := FindServerByID(context.Background(), "reference", "no-such-server", nil)
	assert.ErrorIs(t, err, ErrServerNotFound)
}

// An exact id always wins over the source-prefixed alias, so a listing that
// holds both "x" and "<source>/x" never resolves "x" to the other entry.
func TestFindServerByIDInSource_ExactMatchWins(t *testing.T) {
	servers := []ServerEntry{{ID: "src/x", Name: "alias"}, {ID: "x", Name: "exact"}}
	got, err := findServerByIDInSource(servers, "src", "x")
	require.NoError(t, err)
	assert.Equal(t, "exact", got.Name)

	got, err = findServerByIDInSource([]ServerEntry{{ID: "src/x"}}, "src", "x")
	require.NoError(t, err)
	assert.Equal(t, "src/x", got.ID)

	_, err = findServerByIDInSource([]ServerEntry{{ID: "other/x"}}, "src", "x")
	assert.ErrorIs(t, err, ErrServerNotFound)
}
