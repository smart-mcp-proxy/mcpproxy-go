package secret

// DenyIdentity is a reserved file identified by device and inode (volume
// serial and file index on Windows), captured when it is statted so a later
// rename cannot change which file it names.
type DenyIdentity struct {
	id fileIdentity
	ok bool
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
	id, ok := identityOfPath(resolved)
	if !ok {
		return DenyIdentity{}, false
	}
	return DenyIdentity{id: id, ok: true}, true
}

// matches reports whether the opened file's identity is this one.
func (d DenyIdentity) matches(id fileIdentity) bool {
	return d.ok && d.id == id
}
