//go:build freebsd || netbsd

package codescripts

import (
	"io/fs"
	"syscall"
	"time"
)

// dirGenerationOf reads a directory's generation stamp from its Lstat result.
// The inode and ctime come from the platform stat structure, whose ctime
// field is spelled Ctimespec here.
func dirGenerationOf(info fs.FileInfo) dirGeneration {
	gen := dirGeneration{modTime: info.ModTime(), size: info.Size()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		gen.ino = uint64(st.Ino)
		gen.changeTime = time.Unix(st.Ctimespec.Unix())
	}
	return gen
}
