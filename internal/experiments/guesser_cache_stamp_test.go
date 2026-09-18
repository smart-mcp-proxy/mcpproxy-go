package experiments

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 105 FR-002 (gap FR001-G2, task T029): the guesser is one of the two
// writers of INTERNAL cache entries (`npm:<pkg>`). It must stamp every entry
// it writes with the internal caller kind so read_cache refuses the entry for
// every caller without evicting it — and its own read path, which goes
// through the ungated Get, must keep serving the stamped entry.
//
// The kind is spelled as its wire value so this test compiles against the
// pre-feature cache package; the constant lives in internal/cache.
const internalCallerKindWire = "internal"

// failingTransport makes any real network attempt an error, so a cache hit is
// the only way checkNPMPackageWithClient can answer with Exists=true.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("network disabled in test")
}

func TestGuesser_CacheEntriesAreStampedInternal(t *testing.T) {
	guesser, db := setupTestGuesser(t)
	defer db.Close()

	const pkg = "@acme/mcp-server-stamp-test"
	const key = "npm:" + pkg
	guesser.cacheInfo(key, &RepositoryInfo{
		Type: RepoTypeNPM, PackageName: pkg, Exists: true, Version: "1.2.3",
		InstallCmd: "npm install " + pkg,
	})

	rec, ok := guesser.cacheManager.Peek(key)
	require.True(t, ok, "the guesser writer must persist its entry")
	require.NotNil(t, rec.Producer, "a guesser entry must carry a producer stamp: an unstamped entry is legacy provenance and would be evicted on the first read_cache probe")
	assert.Equal(t, internalCallerKindWire, rec.Producer.CallerKind, "guesser entries are internal entries")

	// The internal reader is unaffected by the stamp: a cache hit answers
	// without touching the network.
	info := guesser.checkNPMPackageWithClient(context.Background(), pkg, &http.Client{Transport: failingTransport{}})
	require.NotNil(t, info)
	assert.True(t, info.Exists, "the stamped entry must still be served to the guesser's own read path")
	assert.Equal(t, "1.2.3", info.Version)
}
