package secret

import (
	"errors"
	"io/fs"
	"os"
	"sync"
)

// maxSupersededProxyFiles bounds the process-lifetime list of replaced proxy
// file identities (16 bytes each). The oldest entry is dropped first.
const maxSupersededProxyFiles = 4096

// supersededStamp is a replaced proxy file: its identity plus its last
// modification time. Linux and Windows reuse a freed inode / file index, so
// the identity alone would, once the replaced file is gone, refuse an
// unrelated file that later got the same identity (for the life of the
// process). A hardlink to the replaced file keeps its modification time
// (unlinking and renaming change only ctime); a new file at a reused identity
// gets its own.
type supersededStamp struct {
	id    fileIdentity
	mtime int64 // nanoseconds since the epoch
}

var (
	supersededMu    sync.Mutex
	supersededFiles []supersededStamp
	supersededSet   = map[supersededStamp]struct{}{}
)

// NoteSupersededProxyFile records the identity path has now, just before a
// writer renames a new file over it (SaveConfig and the deprecated-key
// cleanup do this for mcp_config.json). After the rename the old inode is no
// longer reachable through path, so the live deny list stops matching it, but
// a hardlink made to it earlier still reads the old content (api_key, server
// entries). Upstream reads refuse every identity recorded here for the life of
// the process. The identity is captured now (device and inode, or volume
// serial and file index read from a handle on Windows), never from a retained
// FileInfo, together with the file's modification time from the same stat
// (see supersededStamp). A path that does not exist records nothing and is
// not an error (the first save creates the file).
//
// A write to the replaced file through a hardlink, or setting its times,
// changes that time and ends the refusal; either needs ownership of or write
// access (FILE_WRITE_ATTRIBUTES on Windows) to the proxy's own 0600 config,
// which only its owner has, and the owner can read it anyway. On a filesystem
// with coarse timestamps a new file at a reused identity can still share the
// time and be refused: the rare residue of the false refusal this avoids, and
// it fails closed.
//
// Any other failure to read the identity is returned, and nothing is
// recorded: the caller should warn, since a hardlink to the replaced file will
// not be refused. The error names the path and the reason, never content.
//
// Best effort: the record is in memory only, so a restart, a save by another
// process (a separate CLI invocation) or more than maxSupersededProxyFiles
// later saves forget it.
func NoteSupersededProxyFile(path string) error {
	if path == "" {
		return nil
	}
	st, err := stampOfPathErr(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	recordSupersededStamp(st)
	return nil
}

func recordSupersededStamp(st supersededStamp) {
	supersededMu.Lock()
	defer supersededMu.Unlock()
	if _, known := supersededSet[st]; known {
		return
	}
	if len(supersededFiles) >= maxSupersededProxyFiles {
		delete(supersededSet, supersededFiles[0])
		supersededFiles = append(supersededFiles[:0:0], supersededFiles[1:]...)
	}
	supersededFiles = append(supersededFiles, st)
	supersededSet[st] = struct{}{}
}

// matchesSupersededProxyFile reports whether the opened file is one recorded
// by NoteSupersededProxyFile (same identity and modification time). An identity that cannot be read is treated as a
// match (fail closed).
func matchesSupersededProxyFile(f *os.File, info os.FileInfo) bool {
	st, ok := stampOfHandle(f, info)
	if !ok {
		return true
	}
	supersededMu.Lock()
	defer supersededMu.Unlock()
	_, known := supersededSet[st]
	return known
}
