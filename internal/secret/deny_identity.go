package secret

import (
	"os"
)

// DenyIdentity is a reserved file identified by device and inode (SameFile).
type DenyIdentity struct {
	fi os.FileInfo
}

// StatDenyIdentity records a file to refuse, following symlinks like a secret read.
func StatDenyIdentity(path string) (DenyIdentity, bool) {
	if path == "" {
		return DenyIdentity{}, false
	}
	resolved, err := resolveSymlinks(path)
	if err != nil {
		resolved = path
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return DenyIdentity{}, false
	}
	if !fi.Mode().IsRegular() {
		return DenyIdentity{}, false
	}
	return DenyIdentity{fi: fi}, true
}

// Matches reports whether info refers to the same file as this identity.
func (id DenyIdentity) Matches(info os.FileInfo) bool {
	if id.fi == nil || info == nil {
		return false
	}
	return os.SameFile(id.fi, info)
}
