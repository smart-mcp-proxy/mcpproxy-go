//go:build freebsd || netbsd

package codescripts

import (
	"io/fs"
	"syscall"
	"time"
)

// dirGenerationOf reads a directory's generation stamp from its Lstat result.
// The inode, device and ctime come from the platform stat structure, whose
// ctime field is spelled Ctimespec here. The device is part of the stamp
// (round 9 MUST-FIX): an inode number is unique only within its device, so
// without it a bind-mount swap to another filesystem could collide on inode,
// size and both timestamps.
func dirGenerationOf(info fs.FileInfo) dirGeneration {
	gen := dirGeneration{modTime: info.ModTime(), size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		gen.ino = uint64(st.Ino)
		gen.dev = uint64(st.Dev)
		gen.changeTime = time.Unix(st.Ctimespec.Unix())
	}
	return gen
}
