package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	skillindex "github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 (skills over MCP), PR-3, plan D11/D12: what an agent may see of a
// skill. Everything here answers from the APPROVED copy only and never
// contacts an upstream (FR-008b). Each accessor reads the records once and
// decides availability on that same record.

// ErrSkillOutputBlocked is returned for skill text the output sanitisation
// policy does not let out.
var ErrSkillOutputBlocked = errors.New("skill output blocked by the output sanitisation policy")

// SkillReach says whether a caller may reach a server; nil means every server.
type SkillReach func(serverName string) bool

func (f SkillReach) allows(serverName string) bool { return f == nil || f(serverName) }

// AvailableSkill is a skill as an agent sees it: built from the approved copy.
type AvailableSkill struct {
	ID           string // "server:skill-path"
	Server       string
	Path         string
	URI          string
	Name         string
	Description  string
	ApprovedHash string
	Files        []storage.SkillFile
	TotalSize    int64
}

// publishSkillsChanged announces a change of a server's skills and moves the
// search index generation, so the next search rebuilds.
func (r *Runtime) publishSkillsChanged(serverName string, counts map[string]int) {
	r.skills.bumpIndexGeneration()
	r.publishEvent(newEvent(EventTypeSkillsChanged, map[string]any{"server": serverName, "counts": counts}))
}

func (s *skillsCatalogState) bumpIndexGeneration() { s.indexGen.Add(1) }

func (s *skillsCatalogState) closeIndex() {
	s.idxMu.Lock()
	defer s.idxMu.Unlock()
	s.idxDone = true
	for k, e := range s.idxCache {
		_ = e.ix.Close()
		delete(s.idxCache, k)
	}
}

// approvedNameDescription is the name and description of the approved copy,
// from its frontmatter, with the rules the listing uses.
func approvedNameDescription(rec *storage.SkillApprovalRecord) (name, desc string) {
	name = rec.SkillPath[strings.LastIndex(rec.SkillPath, "/")+1:]
	if rec.Approved == nil || len(rec.Approved.Frontmatter) == 0 {
		return name, ""
	}
	var fm map[string]any
	if err := json.Unmarshal(rec.Approved.Frontmatter, &fm); err != nil {
		return name, ""
	}
	if n, ok := fm["name"].(string); ok && n != "" {
		name = cutText(n, maxSkillNameBytes)
	}
	if d, ok := fm["description"].(string); ok {
		desc = cutText(d, maxSkillDescriptionBytes)
	}
	return name, desc
}

func availableSkillOf(serverName string, rec *storage.SkillApprovalRecord) *AvailableSkill {
	name, desc := approvedNameDescription(rec)
	a := &AvailableSkill{
		ID:           serverName + ":" + rec.SkillPath,
		Server:       serverName,
		Path:         rec.SkillPath,
		URI:          rec.SkillURI,
		Name:         name,
		Description:  desc,
		ApprovedHash: rec.ApprovedHash,
		Files:        append([]storage.SkillFile(nil), rec.Approved.Files...),
	}
	for _, f := range a.Files {
		a.TotalSize += f.Size
	}
	return a
}

func skillNotFound(serverName, skillPath string) error {
	return fmt.Errorf("%w: %s:%s", ErrSkillNotFound, serverName, skillPath)
}

// availableRecord reads the server's records once and returns the one for the
// path when it is available to agents, nil for every other reason.
func (r *Runtime) availableRecord(serverName, skillPath string) (*storage.SkillApprovalRecord, error) {
	cfg := r.Config()
	if cfg == nil || !cfg.EnableSkills || !skillsServerExposed(cfg, skillsServerConfig(cfg, serverName)) {
		return nil, nil
	}
	recs, err := r.ListSkillRecords(serverName)
	if err != nil {
		return nil, err
	}
	rec := skillByPath(recs, skillPath)
	if rec == nil || rec.Approved == nil || !skillAvailableIn(cfg, serverName, rec) {
		return nil, nil
	}
	return rec, nil
}

// AvailableSkill returns the approved copy of a skill, or ErrSkillNotFound for
// every reason the skill is not available to agents.
func (r *Runtime) AvailableSkill(serverName, skillPath string) (*AvailableSkill, error) {
	rec, err := r.availableRecord(serverName, skillPath)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, skillNotFound(serverName, skillPath)
	}
	return availableSkillOf(serverName, rec), nil
}

// ListAvailableSkills returns the approved copies of every skill of a server
// that is available to agents, sorted by path. Nothing else about the server's
// skills is visible through it. A server that is excluded, or the feature
// being off, gives an empty list.
func (r *Runtime) ListAvailableSkills(serverName string) ([]*AvailableSkill, error) {
	cfg := r.Config()
	if cfg == nil || !cfg.EnableSkills || !skillsServerExposed(cfg, skillsServerConfig(cfg, serverName)) {
		return nil, nil
	}
	recs, err := r.ListSkillRecords(serverName)
	if err != nil {
		return nil, err
	}
	var out []*AvailableSkill
	for _, rec := range recs {
		if rec.Approved != nil && skillAvailableIn(cfg, serverName, rec) {
			out = append(out, availableSkillOf(serverName, rec))
		}
	}
	return out, nil
}

// AvailableSkillFile returns one text file of the approved copy of an
// available skill, re-verified against its digest and size. relPath is matched
// exactly against the approved file set (no path cleaning). Errors:
// ErrSkillNotFound (skill unavailable or file not in the approved set),
// ErrSkillFileBinary, ErrSkillStoredCopyDamaged (the record is then marked
// unusable).
func (r *Runtime) AvailableSkillFile(serverName, skillPath, relPath string) ([]byte, storage.SkillFile, *AvailableSkill, error) {
	rec, err := r.availableRecord(serverName, skillPath)
	if err != nil {
		return nil, storage.SkillFile{}, nil, err
	}
	if rec == nil {
		return nil, storage.SkillFile{}, nil, skillNotFound(serverName, skillPath)
	}
	data, file, err := r.readSkillFileOf(serverName, rec, rec.Approved, relPath, true)
	if errors.Is(err, ErrSkillStoredCopyDamaged) {
		// The record was read before the bytes. If another version was
		// approved in between, the bytes of the version read here may be gone
		// for a good reason: read once more, from the record as it is now.
		// A skill that is not available any more (the damage was real and
		// it is unusable now) keeps the damaged answer of this read.
		if again, aerr := r.availableRecord(serverName, skillPath); aerr == nil && again != nil && again.ApprovedHash != rec.ApprovedHash {
			rec = again
			data, file, err = r.readSkillFileOf(serverName, rec, rec.Approved, relPath, true)
		}
	}
	if errors.Is(err, ErrSkillFileNotFound) {
		return nil, storage.SkillFile{}, nil, skillNotFound(serverName, skillPath)
	}
	if err != nil {
		return nil, file, nil, err
	}
	return data, file, availableSkillOf(serverName, rec), nil
}

// maxSkillQueryBytes bounds the text a caller can make the index analyse.
const maxSkillQueryBytes = 512

// maxSkillIndexes bounds how many reach-specific indexes are kept. Callers
// with the same reach share one; the least recently used is dropped.
const maxSkillIndexes = 16

// skillIndexEntry is the index of the skills one set of servers offers.
type skillIndexEntry struct {
	ix    *skillindex.Index
	built uint64
	used  uint64
}

// skillIndexFor returns the index that holds exactly the available skills of
// servers (sorted names), rebuilt when gen moved past the generation it was
// built from. The caller holds idxMu.
//
// One index per reach, not one for everybody: a full-text score depends on how
// often a word occurs in the whole index, so in a shared index the order of a
// caller's own results would move with skills on servers it cannot reach.
func (r *Runtime) skillIndexFor(cfg *config.Config, gen uint64, servers []string) (*skillindex.Index, error) {
	s := &r.skills
	key := strings.Join(servers, "\x00")
	s.idxTick++
	e := s.idxCache[key]
	if e != nil && e.built == gen {
		e.used = s.idxTick
		return e.ix, nil
	}
	var docs []skillindex.Doc
	for _, name := range servers {
		recs, err := r.ListSkillRecords(name)
		if err != nil {
			return nil, err
		}
		resolved := skillsByPath(recs)
		for _, rec := range recs {
			// Only the record its path resolves to, so an id found by search
			// is the id load_skill finds.
			if rec.Approved == nil || !skillAvailableIn(cfg, name, rec) || resolved[rec.SkillPath] != rec {
				continue
			}
			n, desc := approvedNameDescription(rec)
			docs = append(docs, skillindex.Doc{
				ID: name + ":" + rec.SkillPath, Server: name, Path: rec.SkillPath, URI: rec.SkillURI,
				Name: n, Description: desc,
			})
		}
	}
	if e == nil {
		ix, err := skillindex.New()
		if err != nil {
			return nil, err
		}
		if s.idxCache == nil {
			s.idxCache = map[string]*skillIndexEntry{}
		}
		for len(s.idxCache) >= maxSkillIndexes {
			oldest := ""
			for k, c := range s.idxCache {
				if oldest == "" || c.used < s.idxCache[oldest].used {
					oldest = k
				}
			}
			_ = s.idxCache[oldest].ix.Close()
			delete(s.idxCache, oldest)
		}
		e = &skillIndexEntry{ix: ix}
		s.idxCache[key] = e
	}
	if err := e.ix.Replace(docs); err != nil {
		return nil, err
	}
	e.built, e.used = gen, s.idxTick
	return e.ix, nil
}

// maxSkillSearchMatches bounds how many matches of one search are checked
// against their records. A search with more matches reports the checked ones:
// a total that is too low is safe, one that is too high could count a held
// skill.
const maxSkillSearchMatches = 1000

// SearchAvailableSkills searches the skills available to a caller. total is the
// number of matches among the skills available to THIS caller (reach applied),
// never a wider count; hits are the best limit of them. The index it searches
// holds the skills of the caller's reachable servers and nothing else.
//
// The index only proposes: EVERY match, returned or not, is checked against
// its live record before it is counted, because the index may be a moment
// older than the records (a block that has been written but not yet announced).
// A skill that is held now is therefore never returned and never counted. With
// the feature off there is nothing: no hits, total 0.
func (r *Runtime) SearchAvailableSkills(query string, limit int, reach SkillReach) (hits []AvailableSkill, total int, err error) {
	query = cutText(strings.TrimSpace(query), maxSkillQueryBytes)
	if query == "" || limit <= 0 || r.storageManager == nil {
		return nil, 0, nil
	}
	s := &r.skills
	s.idxMu.Lock()
	if s.idxDone {
		s.idxMu.Unlock()
		return nil, 0, nil
	}
	// The generation is read before the configuration and the records: a
	// change that lands after this point moves the generation past the one
	// this build is stamped with, so the next search rebuilds.
	gen := s.indexGen.Load()
	cfg := r.Config()
	if cfg == nil || !cfg.EnableSkills {
		s.idxMu.Unlock()
		return nil, 0, nil
	}
	var servers []string
	for _, sc := range cfg.Servers {
		if sc != nil && skillsServerExposed(cfg, sc) && reach.allows(sc.Name) {
			servers = append(servers, sc.Name)
		}
	}
	if len(servers) == 0 {
		s.idxMu.Unlock()
		return nil, 0, nil
	}
	sort.Strings(servers)
	ix, err := r.skillIndexFor(cfg, gen, servers)
	if err != nil {
		s.idxMu.Unlock()
		return nil, 0, err
	}
	// Searching under the lock keeps Close from releasing the index mid-search.
	raw, _, err := ix.Search(query, maxSkillSearchMatches)
	s.idxMu.Unlock()
	if err != nil {
		return nil, 0, err
	}

	// One keyed record read per match: the cost of a search is bounded by the
	// matches, not by the records a server has accumulated.
	stale := false
	for _, h := range raw {
		rec, gerr := r.storageManager.GetSkillApproval(h.Server, h.URI)
		if gerr != nil || rec == nil || rec.Approved == nil || rec.SkillPath != h.Path ||
			!reach.allows(h.Server) || !skillAvailableIn(cfg, h.Server, rec) {
			stale = true
			continue
		}
		total++
		if len(hits) < limit {
			hits = append(hits, *availableSkillOf(h.Server, rec))
		}
	}
	if stale {
		// The index is older than the records: the next search rebuilds it.
		s.bumpIndexGeneration()
	}
	return hits, total, nil
}
