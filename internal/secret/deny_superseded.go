package secret

import (
	"os"
	"sync"
)

// maxSupersededProxyFiles bounds the process-lifetime list of replaced proxy
// file identities (16 bytes each). The oldest entry is dropped first.
const maxSupersededProxyFiles = 4096

var (
	supersededMu    sync.Mutex
	supersededFiles []fileIdentity
	supersededSet   = map[fileIdentity]struct{}{}
)

// NoteSupersededProxyFile records the identity path has now, just before a
// writer renames a new file over it (SaveConfig and the deprecated-key
// cleanup do this for mcp_config.json). After the rename the old inode is no
// longer reachable through path, so the live deny list stops matching it, but
// a hardlink made to it earlier still reads the old content (api_key, server
// entries). Upstream reads refuse every identity recorded here for the life of
// the process. The identity is captured now (device and inode, or volume
// serial and file index read from a handle on Windows), never from a retained
// FileInfo. A path that does not exist or is not a regular file records
// nothing.
//
// Best effort: the record is in memory only, so a restart, a save by another
// process (a separate CLI invocation) or more than maxSupersededProxyFiles
// later saves forget it.
func NoteSupersededProxyFile(path string) {
	if path == "" {
		return
	}
	id, ok := identityOfPath(path)
	if !ok {
		return
	}
	supersededMu.Lock()
	defer supersededMu.Unlock()
	if _, known := supersededSet[id]; known {
		return
	}
	if len(supersededFiles) >= maxSupersededProxyFiles {
		delete(supersededSet, supersededFiles[0])
		supersededFiles = append(supersededFiles[:0:0], supersededFiles[1:]...)
	}
	supersededFiles = append(supersededFiles, id)
	supersededSet[id] = struct{}{}
}

// matchesSupersededProxyFile reports whether the opened file is one recorded
// by NoteSupersededProxyFile. An identity that cannot be read is treated as a
// match (fail closed).
func matchesSupersededProxyFile(f *os.File, info os.FileInfo) bool {
	id, ok := identityOfHandle(f, info)
	if !ok {
		return true
	}
	supersededMu.Lock()
	defer supersededMu.Unlock()
	_, known := supersededSet[id]
	return known
}
