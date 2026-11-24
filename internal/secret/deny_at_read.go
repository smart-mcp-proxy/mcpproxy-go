package secret

import (
	"os"
	"path/filepath"
)

// Two passes pick the files compared with the opened handle.
//
// Pass 1 is the original, unbudgeted coverage: every non-directory entry
// directly in the data dir, and every non-directory entry directly in certs/,
// index.bleve/ and tpa-lkg/. It cannot be starved by anything below.
//
// Pass 2 extends it deeper. Its cost must not depend on how large the data dir
// has grown (the Bleve index holds many segment files), so it has fixed
// budgets: at most maxDenyWalkDepth directory levels and maxDenyWalkEntries
// directory entries per read, read in bounded batches (a huge directory is
// never enumerated whole), shallow entries first. It lists only regular files
// and never follows symlinks. A file beyond the budget is still refused by
// path containment (deniedByPath) and, when it has another name, by the
// link-count rule; only a path swapped to it after those checks goes
// unmatched, which the budget trades for a bounded cost.
const (
	maxDenyWalkDepth   = 8
	maxDenyWalkEntries = 4096
)

// dataDirStateSubdirs are data-dir children whose direct files are always
// compared (pass 1).
var dataDirStateSubdirs = []string{"certs", "index.bleve", "tpa-lkg"}

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

	// Pass 1.
	entries, err := os.ReadDir(p.Deny.DataDir)
	if err != nil {
		return uniqueAbsPaths(paths)
	}
	var queue []denyWalkLevel
	for _, e := range entries {
		name := e.Name()
		if name == "secrets" {
			continue
		}
		full := filepath.Join(p.Deny.DataDir, name)
		if !e.IsDir() {
			paths = append(paths, full)
			continue
		}
		covered := false
		for _, sub := range dataDirStateSubdirs {
			if name == sub {
				covered = true
			}
		}
		queue = append(queue, denyWalkLevel{full, 2, covered})
		for _, sub := range dataDirStateSubdirs {
			if name != sub {
				continue
			}
			if subEntries, err := os.ReadDir(full); err == nil {
				for _, se := range subEntries {
					if !se.IsDir() {
						paths = append(paths, filepath.Join(full, se.Name()))
					}
				}
			}
			break
		}
	}

	// Pass 2: breadth-first from the data dir's subdirectories (their direct
	// children were read in pass 1 only for the three state directories).
	budget := maxDenyWalkEntries
	for len(queue) > 0 && budget > 0 {
		cur := queue[0]
		queue = queue[1:]
		budget = walkDenyDir(cur, budget, &paths, &queue)
	}
	return uniqueAbsPaths(paths)
}

type denyWalkLevel struct {
	dir   string
	depth int
	// covered: the direct files of this directory were collected in pass 1, so
	// pass 2 neither re-lists nor charges for them (only its subdirectories).
	covered bool
}

// walkDenyDir reads up to budget entries of one directory in batches and
// returns the budget left.
func walkDenyDir(cur denyWalkLevel, budget int, paths *[]string, queue *[]denyWalkLevel) int {
	budget-- // an empty or unreadable directory still costs its open
	// Never open something that is no longer a directory (a FIFO would block
	// the open); the window between this check and the open needs write access
	// to the data dir, which already allows replacing the config itself.
	if fi, err := os.Lstat(cur.dir); err != nil || !fi.IsDir() {
		return budget
	}
	d, err := os.Open(cur.dir)
	if err != nil {
		return budget
	}
	defer d.Close()
	for budget > 0 {
		batch := budget
		if batch > 256 {
			batch = 256
		}
		entries, err := d.ReadDir(batch)
		for _, e := range entries {
			if cur.covered && e.Type().IsRegular() {
				continue
			}
			budget--
			full := filepath.Join(cur.dir, e.Name())
			switch {
			case e.Type().IsRegular():
				*paths = append(*paths, full)
			case e.IsDir() && cur.depth < maxDenyWalkDepth:
				*queue = append(*queue, denyWalkLevel{full, cur.depth + 1, false})
			}
		}
		if err != nil || len(entries) == 0 {
			break
		}
	}
	return budget
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
