package secret

import (
	"os"
	"path/filepath"
)

// dataDirStateSubdirs are data-dir children that hold proxy state files. Files
// directly inside these directories are refused (not deeper nesting). The
// secrets/ subtree is always excluded.
var dataDirStateSubdirs = []string{
	"certs",
	"index.bleve",
	"tpa-lkg",
}

// denyPathsAtRead returns filesystem paths to stat for handle identity checks.
// Paths are fresh on every read so renames and new inodes are caught.
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
	entries, err := os.ReadDir(p.Deny.DataDir)
	if err != nil {
		return uniqueAbsPaths(paths)
	}
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
		for _, sub := range dataDirStateSubdirs {
			if name != sub {
				continue
			}
			subEntries, err := os.ReadDir(full)
			if err != nil {
				break
			}
			for _, se := range subEntries {
				if !se.IsDir() {
					paths = append(paths, filepath.Join(full, se.Name()))
				}
			}
			break
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
