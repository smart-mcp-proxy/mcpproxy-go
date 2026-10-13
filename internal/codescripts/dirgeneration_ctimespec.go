//go:build freebsd || netbsd || darwin

package codescripts

import (
	"io/fs"
	"syscall"
	"time"
)

// dirGenerationOf reads a directory's generation stamp from its Lstat result.
// The inode, device and ctime come from the platform stat structure, whose
// ctime field is spelled Ctimespec here (freebsd, netbsd, and — round 13,
// darwin's join of the shared Linux/BSD design — darwin too; the standard
// library's syscall.Stat_t spells it Ctimespec on all three, unlike
// x/sys/unix.Stat_t, which dirfd_other.go's fd-based reader uses instead and
// which normalizes the field to Ctim uniformly, darwin included). The device
// is part of the stamp (round 9 MUST-FIX): an inode number is unique only
// within its device, so without it a bind-mount swap to another filesystem
// could collide on inode, size and both timestamps.
func dirGenerationOf(info fs.FileInfo) dirGeneration {
	gen := dirGeneration{modTime: info.ModTime(), size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		gen.ino = uint64(st.Ino)
		gen.dev = uint64(st.Dev)
		gen.changeTime = time.Unix(st.Ctimespec.Unix())
	}
	return gen
}
