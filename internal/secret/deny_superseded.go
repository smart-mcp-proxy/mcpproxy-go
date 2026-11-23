package secret

import (
	"os"
	"sync"
)

// maxSupersededProxyFiles bounds the process-lifetime list of replaced proxy
// file identities. The oldest entry is dropped first.
const maxSupersededProxyFiles = 256

var (
	supersededMu    sync.Mutex
	supersededFiles []os.FileInfo
)

// NoteSupersededProxyFile records the identity path has now, just before a
// writer renames a new file over it (SaveConfig and the deprecated-key
// cleanup do this for mcp_config.json). After the rename the old inode is no
// longer reachable through path, so the live deny list stops matching it, but
// a hardlink made to it earlier still reads the old content (api_key, server
// entries). Upstream reads refuse every identity recorded here for the life of
// the process. A path that does not exist or is not a regular file records
// nothing.
func NoteSupersededProxyFile(path string) {
	if path == "" {
		return
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	supersededMu.Lock()
	defer supersededMu.Unlock()
	for _, known := range supersededFiles {
		if os.SameFile(known, fi) {
			return
		}
	}
	if len(supersededFiles) >= maxSupersededProxyFiles {
		supersededFiles = append(supersededFiles[:0:0], supersededFiles[1:]...)
	}
	supersededFiles = append(supersededFiles, fi)
}

func matchesSupersededProxyFile(info os.FileInfo) bool {
	supersededMu.Lock()
	defer supersededMu.Unlock()
	for _, known := range supersededFiles {
		if os.SameFile(known, info) {
			return true
		}
	}
	return false
}
