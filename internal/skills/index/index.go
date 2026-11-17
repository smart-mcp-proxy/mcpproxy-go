// Package index is a memory-only full-text index of skills (spec 125, D12).
//
// It is built from scratch by Replace and never written to disk. It uses the
// same analyzers as the tool index, so ranking behaves like retrieve_tools.
// The package imports nothing from this module: the runtime decides which
// skills go in and re-checks every hit against live state.
package index

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	bquery "github.com/blevesearch/bleve/v2/search/query"
)

// Doc is one skill to index. ID is "server:skill-path"; URI is the skill's
// address on its server, which is the key of its record.
type Doc struct {
	ID, Server, Path, URI, Name, Description string
}

// Hit is one search result, best first.
type Hit struct {
	ID, Server, Path, URI string
	Score                 float64
}

// ErrClosed is returned by a closed index.
var ErrClosed = errors.New("skills index is closed")

type indexDoc struct {
	Server         string `json:"server"`
	Path           string `json:"path"`
	URI            string `json:"uri"`
	NameExact      string `json:"name_exact"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	SearchableText string `json:"searchable_text"`
}

// Index holds the current generation; Replace swaps it atomically.
type Index struct {
	mu     sync.RWMutex
	cur    bleve.Index
	n      int
	closed bool
}

func buildMapping() *mapping.IndexMappingImpl {
	m := bleve.NewIndexMapping()
	dm := bleve.NewDocumentMapping()
	dm.Dynamic = false
	add := func(name, analyzer string, store bool) {
		f := bleve.NewTextFieldMapping()
		f.Analyzer = analyzer
		f.Store = store
		f.Index = true
		dm.AddFieldMappingsAt(name, f)
	}
	add("server", keyword.Name, true)
	add("path", keyword.Name, true)
	add("uri", keyword.Name, true)
	add("name_exact", keyword.Name, false)
	add("name", standard.Name, false)
	add("description", standard.Name, false)
	add("searchable_text", standard.Name, false)
	m.DefaultMapping = dm
	return m
}

func newBleve(docs []Doc) (bleve.Index, error) {
	idx, err := bleve.NewMemOnly(buildMapping())
	if err != nil {
		return nil, fmt.Errorf("create skills index: %w", err)
	}
	b := idx.NewBatch()
	for _, d := range docs {
		if err := b.Index(d.ID, indexDoc{
			Server:         d.Server,
			Path:           d.Path,
			URI:            d.URI,
			NameExact:      strings.ToLower(d.Name),
			Name:           splitUnderscores(d.Name),
			Description:    splitUnderscores(d.Description),
			SearchableText: splitUnderscores(d.Name + " " + d.Description),
		}); err != nil {
			_ = idx.Close()
			return nil, err
		}
	}
	if err := idx.Batch(b); err != nil {
		_ = idx.Close()
		return nil, err
	}
	return idx, nil
}

// New returns an empty index.
func New() (*Index, error) {
	b, err := newBleve(nil)
	if err != nil {
		return nil, err
	}
	return &Index{cur: b}, nil
}

// Replace builds a fresh index from docs and swaps it in. A search that is
// running sees the old index or the new one, never a half-built one.
func (ix *Index) Replace(docs []Doc) error {
	fresh, err := newBleve(docs)
	if err != nil {
		return err
	}
	ix.mu.Lock()
	if ix.closed {
		ix.mu.Unlock()
		_ = fresh.Close()
		return ErrClosed
	}
	old := ix.cur
	ix.cur, ix.n = fresh, len(docs)
	ix.mu.Unlock()
	// Searches hold the read lock for their whole run, so by the time the
	// write lock above was granted none still uses old.
	_ = old.Close()
	return nil
}

// Len is the number of documents in the current index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.n
}

// Close releases the index; it is safe to call twice.
func (ix *Index) Close() error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.closed {
		return nil
	}
	ix.closed = true
	return ix.cur.Close()
}

// splitUnderscores makes snake_case names searchable by their parts: the
// standard analyzer keeps "my_skill" as one token, so the text fields are
// indexed (and queried) with underscores as spaces. Hyphens already split.
func splitUnderscores(s string) string { return strings.ReplaceAll(s, "_", " ") }

func buildQuery(q string) bquery.Query {
	lower := strings.ToLower(q)
	q = splitUnderscores(q)
	bq := bleve.NewBooleanQuery()

	exact := bleve.NewTermQuery(lower)
	exact.SetField("name_exact")
	exact.SetBoost(5.0)
	bq.AddShould(exact)

	prefix := bleve.NewPrefixQuery(lower)
	prefix.SetField("name_exact")
	prefix.SetBoost(3.0)
	bq.AddShould(prefix)

	name := bleve.NewMatchQuery(q)
	name.SetField("name")
	name.SetBoost(2.5)
	bq.AddShould(name)

	desc := bleve.NewMatchQuery(q)
	desc.SetField("description")
	desc.SetBoost(1.0)
	bq.AddShould(desc)

	text := bleve.NewMatchQuery(q)
	text.SetField("searchable_text")
	text.SetBoost(1.5)
	bq.AddShould(text)
	return bq
}

// Search returns up to limit hits, best first, ties broken by id, and the
// number of documents that match at all. An empty or blank query returns
// nothing.
func (ix *Index) Search(query string, limit int) (hits []Hit, total int, err error) {
	query = strings.TrimSpace(query)
	if query == "" || limit <= 0 {
		return nil, 0, nil
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.closed {
		return nil, 0, ErrClosed
	}
	if limit > ix.n {
		limit = ix.n
	}
	if limit == 0 {
		return nil, 0, nil
	}
	req := bleve.NewSearchRequestOptions(buildQuery(query), limit, 0, false)
	req.Fields = []string{"server", "path", "uri"}
	req.SortBy([]string{"-_score", "_id"})
	res, err := ix.cur.Search(req)
	if err != nil {
		return nil, 0, err
	}
	hits = make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		hit := Hit{ID: h.ID, Score: h.Score}
		hit.Server, _ = h.Fields["server"].(string)
		hit.Path, _ = h.Fields["path"].(string)
		hit.URI, _ = h.Fields["uri"].(string)
		hits = append(hits, hit)
	}
	return hits, int(res.Total), nil //nolint:gosec // bounded by the number of indexed documents
}
