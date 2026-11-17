package index

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newIdx(t *testing.T, docs ...Doc) *Index {
	t.Helper()
	ix, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ix.Close() })
	require.NoError(t, ix.Replace(docs))
	return ix
}

func doc(server, path, name, desc string) Doc {
	return Doc{ID: server + ":" + path, Server: server, Path: path, Name: name, Description: desc}
}

func ids(hits []Hit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, h.ID)
	}
	return out
}

func TestSearchRanksNameOverDescription(t *testing.T) {
	ix := newIdx(t,
		doc("a", "other", "other", "helps you with pdf files and many other things"),
		doc("a", "pdf", "pdf", "reads documents"),
	)
	hits, _, err := ix.Search("pdf", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"a:pdf", "a:other"}, ids(hits))
	require.Greater(t, hits[0].Score, hits[1].Score)
	require.Equal(t, "a", hits[0].Server)
	require.Equal(t, "pdf", hits[0].Path)
}

func TestSearchDescriptionOnlyMatch(t *testing.T) {
	ix := newIdx(t, doc("a", "x", "alpha", "converts spreadsheets into charts"))
	hits, _, err := ix.Search("spreadsheets", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"a:x"}, ids(hits))
}

func TestSearchHyphenAndUnderscoreNames(t *testing.T) {
	ix := newIdx(t,
		doc("a", "hf-cli", "hf-cli", ""),
		doc("a", "pdf-tools", "pdf-tools", ""),
		doc("a", "my_skill", "my_skill", ""),
	)
	for q, want := range map[string]string{"hf cli": "a:hf-cli", "pdf": "a:pdf-tools", "hf-cli": "a:hf-cli", "my skill": "a:my_skill", "my_skill": "a:my_skill", "HF-CLI": "a:hf-cli"} {
		hits, _, err := ix.Search(q, 10)
		require.NoError(t, err, q)
		require.NotEmpty(t, hits, q)
		require.Equal(t, want, hits[0].ID, q)
	}
}

func TestSearchEmptyQuery(t *testing.T) {
	ix := newIdx(t, doc("a", "x", "x", "y"))
	for _, q := range []string{"", "   ", "\t\n"} {
		hits, _, err := ix.Search(q, 10)
		require.NoError(t, err)
		require.Empty(t, hits)
	}
	hits, _, err := ix.Search("x", 0)
	require.NoError(t, err)
	require.Empty(t, hits)
}

func TestReplaceDropsOldDocs(t *testing.T) {
	ix := newIdx(t, doc("a", "old", "oldskill", "legacy"))
	require.Equal(t, 1, ix.Len())
	require.NoError(t, ix.Replace([]Doc{doc("a", "new", "newskill", "fresh")}))
	require.Equal(t, 1, ix.Len())
	hits, _, err := ix.Search("oldskill", 10)
	require.NoError(t, err)
	require.Empty(t, hits)
	hits, _, err = ix.Search("newskill", 10)
	require.NoError(t, err)
	require.Equal(t, []string{"a:new"}, ids(hits))
	require.NoError(t, ix.Replace(nil))
	require.Equal(t, 0, ix.Len())
}

func TestSearchLimitUpToIndexSize(t *testing.T) {
	var docs []Doc
	for i := 0; i < 250; i++ {
		docs = append(docs, doc("s", fmt.Sprintf("k%d", i), fmt.Sprintf("k%d", i), "common words"))
	}
	ix := newIdx(t, docs...)
	hits, _, err := ix.Search("common", 10000)
	require.NoError(t, err)
	require.Len(t, hits, 250)
	hits, _, err = ix.Search("common", 7)
	require.NoError(t, err)
	require.Len(t, hits, 7)
}

func TestSearchThousandDocsFast(t *testing.T) {
	var docs []Doc
	for i := 0; i < 1000; i++ {
		docs = append(docs, doc("s", fmt.Sprintf("skill-%d", i), fmt.Sprintf("skill-%d", i), fmt.Sprintf("does thing number %d with widgets", i)))
	}
	ix := newIdx(t, docs...)
	_, _, err := ix.Search("widgets", 20) // warm
	require.NoError(t, err)
	start := time.Now()
	hits, _, err := ix.Search("skill 500", 20)
	elapsed := time.Since(start)
	require.NoError(t, err)
	require.NotEmpty(t, hits)
	if !testing.Short() && !raceEnabled {
		require.Less(t, elapsed, 100*time.Millisecond)
	}
}

func TestConcurrentSearchDuringReplace(t *testing.T) {
	a := []Doc{doc("s", "one", "alpha", "first generation")}
	b := []Doc{doc("s", "two", "alpha", "second generation"), doc("s", "three", "alpha", "second generation")}
	ix := newIdx(t, a...)
	var stop atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				hits, _, err := ix.Search("alpha", 10)
				if err != nil {
					t.Error(err)
					return
				}
				if n := len(hits); n != 1 && n != 2 {
					t.Errorf("half-built index: %d hits", n)
					return
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			require.NoError(t, ix.Replace(b))
		} else {
			require.NoError(t, ix.Replace(a))
		}
	}
	stop.Store(true)
	wg.Wait()
}

func TestClosedIndex(t *testing.T) {
	ix, err := New()
	require.NoError(t, err)
	require.NoError(t, ix.Close())
	require.NoError(t, ix.Close())
	_, _, err = ix.Search("x", 1)
	require.Error(t, err)
	require.Error(t, ix.Replace(nil))
}

func TestSearch_ReportsTheRecordAddressAndTheNumberOfMatches(t *testing.T) {
	ix, err := New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = ix.Close() })
	var docs []Doc
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("srv:common-%02d", i)
		docs = append(docs, Doc{ID: id, Server: "srv", Path: id[4:], URI: "skill://" + id[4:] + "/SKILL.md", Name: id[4:], Description: "common words"})
	}
	docs = append(docs, Doc{ID: "srv:other", Server: "srv", Path: "other", URI: "skill://other/SKILL.md", Name: "other", Description: "nothing shared"})
	require.NoError(t, ix.Replace(docs))

	hits, total, err := ix.Search("common", 5)
	require.NoError(t, err)
	require.Len(t, hits, 5)
	require.Equal(t, 30, total, "total counts every match, not only the returned ones")
	for _, h := range hits {
		require.Equal(t, "skill://"+h.Path+"/SKILL.md", h.URI)
	}
}
