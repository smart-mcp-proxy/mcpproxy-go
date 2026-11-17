package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	skillindex "github.com/smart-mcp-proxy/mcpproxy-go/internal/skills/index"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Spec 125 (skills over MCP), PR-3, plan D11/D12: what an agent may see of a
// skill. Everything here answers from the APPROVED copy only and never
// contacts an upstream (FR-008b). Each accessor reads the records once and
// decides availability on that same record.

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
	if s.idx != nil {
		_ = s.idx.Close()
		s.idx = nil
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
	if errors.Is(err, ErrSkillFileNotFound) {
		return nil, storage.SkillFile{}, nil, skillNotFound(serverName, skillPath)
	}
	if err != nil {
		return nil, file, nil, err
	}
	return data, file, availableSkillOf(serverName, rec), nil
}

// ensureSkillIndex rebuilds the search index when the configuration or a
// skills change moved past the generation it was built from. The caller holds
// idxMu. It returns nil when the runtime is closing.
func (r *Runtime) ensureSkillIndex(cfg *config.Config) (*skillindex.Index, error) {
	s := &r.skills
	if s.idxDone {
		return nil, nil
	}
	gen := s.indexGen.Load()
	if s.idx != nil && s.idxBuilt == gen {
		return s.idx, nil
	}
	var docs []skillindex.Doc
	seen := map[string]bool{}
	for _, sc := range cfg.Servers {
		if sc == nil || !skillsServerExposed(cfg, sc) {
			continue
		}
		recs, err := r.ListSkillRecords(sc.Name)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if rec.Approved == nil || !skillAvailableIn(cfg, sc.Name, rec) {
				continue
			}
			id := sc.Name + ":" + rec.SkillPath
			if seen[id] {
				continue
			}
			seen[id] = true
			name, desc := approvedNameDescription(rec)
			docs = append(docs, skillindex.Doc{ID: id, Server: sc.Name, Path: rec.SkillPath, Name: name, Description: desc})
		}
	}
	if s.idx == nil {
		ix, err := skillindex.New()
		if err != nil {
			return nil, err
		}
		s.idx = ix
	}
	if err := s.idx.Replace(docs); err != nil {
		return nil, err
	}
	s.idxBuilt = gen
	return s.idx, nil
}

// SearchAvailableSkills searches the skills available to a caller. total is the
// number of matches among the skills available to THIS caller (reach applied),
// never a wider count; hits are the best limit of them. Every hit is checked
// again against the live records and the reach, because the index may be a
// moment old. With the feature off there is nothing: no hits, total 0.
func (r *Runtime) SearchAvailableSkills(query string, limit int, reach SkillReach) (hits []AvailableSkill, total int, err error) {
	cfg := r.Config()
	if cfg == nil || !cfg.EnableSkills || strings.TrimSpace(query) == "" {
		return nil, 0, nil
	}
	s := &r.skills
	s.idxMu.Lock()
	ix, err := r.ensureSkillIndex(cfg)
	if err != nil || ix == nil {
		s.idxMu.Unlock()
		return nil, 0, err
	}
	// Searching under the lock keeps Close from releasing the index mid-search.
	raw, err := ix.Search(query, ix.Len())
	s.idxMu.Unlock()
	if err != nil {
		return nil, 0, err
	}

	byServer := map[string][]*storage.SkillApprovalRecord{}
	for _, h := range raw {
		if !reach.allows(h.Server) {
			continue
		}
		recs, ok := byServer[h.Server]
		if !ok {
			if recs, err = r.ListSkillRecords(h.Server); err != nil {
				return nil, 0, err
			}
			byServer[h.Server] = recs
		}
		rec := skillByPath(recs, h.Path)
		if rec == nil || rec.Approved == nil || !skillAvailableIn(cfg, h.Server, rec) {
			continue
		}
		total++
		if len(hits) < limit {
			hits = append(hits, *availableSkillOf(h.Server, rec))
		}
	}
	return hits, total, nil
}
