package secret

import (
	"os"
	"path/filepath"
)

// The data directory is walked breadth-first for files to compare with the
// opened handle. Cost must not depend on how large the data dir has grown
// (the Bleve index holds many segment files), so the walk has fixed budgets:
// at most maxDenyWalkDepth directory levels and maxDenyWalkEntries directory
// entries per read, shallow entries first. A file beyond the budget is still
// refused by path containment (deniedByPath) and, when it has another name,
// by the link-count rule; only a path swapped to it after those checks goes
// unmatched, which the budget trades for a bounded cost.
const (
	maxDenyWalkDepth   = 8
	maxDenyWalkEntries = 4096
)

// denyPathsAtRead returns filesystem paths to stat for handle identity checks.
// Paths are fresh on every read so renames and new inodes are caught. The
// secrets/ subtree is always excluded.
func (p *FilePolicy) denyPathsAtRead() []string {
	if p == nil || p.Scope != ScopeUpstream {
		return nil
	}
	var paths []string
	if p.Deny.ConfigFile != "" {
		paths = append(paths, p.Deny.ConfigFile)
	}
	paths = append(paths, p.Deny.AdminKeyPaths...)
	if p.Deny.DataDir == "" {
		return uniqueAbsPaths(paths)
	}
	type level struct {
		dir   string
		depth int
	}
	queue := []level{{p.Deny.DataDir, 1}}
	budget := maxDenyWalkEntries
	for len(queue) > 0 && budget > 0 {
		cur := queue[0]
		queue = queue[1:]
		entries, err := os.ReadDir(cur.dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if budget == 0 {
				break
			}
			budget--
			full := filepath.Join(cur.dir, e.Name())
			if cur.depth == 1 && e.Name() == "secrets" {
				continue
			}
			if !e.IsDir() {
				paths = append(paths, full)
				continue
			}
			if cur.depth < maxDenyWalkDepth {
				queue = append(queue, level{full, cur.depth + 1})
			}
		}
	}
	return uniqueAbsPaths(paths)
}

func uniqueAbsPaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	var out []string
	for _, path := range paths {
		if path == "" {
			continue
		}
		path = filepath.Clean(path)
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

func (p *FilePolicy) deniedByHandleAtRead(f *os.File, info os.FileInfo) bool {
	if info == nil || p == nil || p.Scope != ScopeUpstream {
		return false
	}
	// The opened file's identity, read from the handle (fail closed when it
	// cannot be read).
	id, ok := identityOfHandle(f, info)
	if !ok {
		return true
	}
	if matchesSupersededProxyFile(f, info) {
		return true
	}
	for _, path := range p.denyPathsAtRead() {
		if d, ok := StatDenyIdentity(path); ok && d.matches(id) {
			return true
		}
	}
	return false
}
