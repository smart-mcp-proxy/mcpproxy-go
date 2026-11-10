package secret

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// NewWildcardFilePolicy returns a policy that allows any absolute regular file.
// Used by NewResolver() so existing tests keep #1717 behaviour.
func NewWildcardFilePolicy(scope ResolveScope) *FilePolicy {
	return &FilePolicy{Wildcard: true, Scope: scope}
}

// NewDisabledFilePolicy refuses every ${file:} reference.
func NewDisabledFilePolicy(scope ResolveScope) *FilePolicy {
	return &FilePolicy{Disabled: true, Scope: scope}
}

// containsRawDotDot reports whether path has a .. path element before cleaning.
func containsRawDotDot(path string) bool {
	for _, elem := range splitPathElements(path) {
		if elem == ".." {
			return true
		}
	}
	return false
}

func splitPathElements(path string) []string {
	if path == "" {
		return nil
	}
	vol := filepath.VolumeName(path)
	trimmed := strings.TrimPrefix(path, vol)
	trimmed = strings.TrimPrefix(trimmed, string(filepath.Separator))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, string(filepath.Separator))
}

// resolveSymlinks wraps filepath.EvalSymlinks; a missing final component is not
// an error here because containment may still fail uniformly.
func resolveSymlinks(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return filepath.Clean(path), nil
		}
		return "", err
	}
	return resolved, nil
}

// pathContainedInRoot reports whether filePath is inside rootPath by path
// element (not string prefix). Both paths must be clean; rootPath is a directory.
func pathContainedInRoot(rootPath, filePath string) (bool, error) {
	if !filepath.IsAbs(rootPath) || !filepath.IsAbs(filePath) {
		return false, fmt.Errorf("containment requires absolute paths")
	}
	rootPath = filepath.Clean(rootPath)
	filePath = filepath.Clean(filePath)

	if pathEqual(rootPath, filePath) {
		return false, nil
	}

	rel, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return false, err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, nil
	}
	first := rel
	if i := strings.IndexByte(rel, filepath.Separator); i >= 0 {
		first = rel[:i]
	}
	if first == ".." {
		return false, nil
	}
	return true, nil
}

func pathEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// matchingRoot returns the allowed root that contains resolvedPath, or "".
func (p *FilePolicy) matchingRoot(resolvedPath string) (string, error) {
	for _, root := range p.AllowedRoots {
		resolvedRoot, err := resolveSymlinks(root)
		if err != nil {
			continue
		}
		ok, err := pathContainedInRoot(resolvedRoot, resolvedPath)
		if err != nil {
			return "", err
		}
		if ok {
			return resolvedRoot, nil
		}
	}
	return "", nil
}

func (p *FilePolicy) deniedByPath(resolvedPath string) bool {
	if resolvedPath == "" || p.Scope != ScopeUpstream {
		return false
	}
	deny := p.Deny
	if deny.ConfigFile != "" && pathEqual(resolvedPath, deny.ConfigFile) {
		return true
	}
	if deny.DataDir != "" {
		if deny.DataSecretsDir != "" {
			if contained, _ := pathContainedInRoot(deny.DataSecretsDir, resolvedPath); contained {
				return false
			}
		}
		if contained, _ := pathContainedInRoot(deny.DataDir, resolvedPath); contained {
			return true
		}
	}
	return false
}


// ReadFileWithPolicy reads a secret file according to policy.
func ReadFileWithPolicy(path string, policy *FilePolicy) (string, error) {
	if path == "" {
		return "", fmt.Errorf("secret file path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("secret file path %q must be absolute", path)
	}
	if err := rejectPlatformAbsolutePath(path); err != nil {
		return "", err
	}
	if containsRawDotDot(path) {
		return "", ErrSecretFileDenied
	}

	resolved, err := resolveSymlinks(path)
	if err != nil {
		return "", ErrSecretFileDenied
	}

	if policy != nil {
		if policy.Disabled {
			return "", ErrSecretFileDenied
		}
		if policy.deniedByPath(resolved) {
			return "", ErrSecretFileDenied
		}
		if !policy.Wildcard {
			root, err := policy.matchingRoot(resolved)
			if err != nil {
				return "", ErrSecretFileDenied
			}
			if root == "" {
				return "", ErrSecretFileDenied
			}
			return readRegularFileFromRoot(policy, root, resolved)
		}
	}
	return readRegularFileAt(policy, resolved)
}

func readRegularFileFromRoot(policy *FilePolicy, rootPath, resolvedPath string) (string, error) {
	rel, err := filepath.Rel(rootPath, resolvedPath)
	if err != nil {
		return "", ErrSecretFileDenied
	}
	rel = filepath.ToSlash(strings.TrimPrefix(rel, string(filepath.Separator)))
	if rel == ".." || strings.HasPrefix(rel, "../") || rel == "" {
		return "", ErrSecretFileDenied
	}
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		return "", ErrSecretFileDenied
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return "", ErrSecretFileDenied
	}
	defer root.Close()

	invokeBeforeSecretOpenHook()

	f, err := openSecretInRoot(root, rel)
	if err != nil {
		return "", ErrSecretFileDenied
	}
	defer f.Close()

	return readOpenedSecret(policy, f, resolvedPath)
}

func readRegularFileAt(policy *FilePolicy, path string) (string, error) {
	invokeBeforeSecretOpenHook()
	f, err := openSecretPath(path)
	if err != nil {
		if policy != nil && (policy.Scope == ScopeUpstream || !policy.Wildcard) {
			return "", ErrSecretFileDenied
		}
		return "", fmt.Errorf("cannot read secret file %q: %w", path, unwrapPathError(err))
	}
	defer f.Close()
	return readOpenedSecret(policy, f, path)
}

func readOpenedSecret(policy *FilePolicy, f *os.File, labelPath string) (string, error) {
	info, err := f.Stat()
	if err != nil {
		if policy != nil && policy.Scope == ScopeUpstream {
			return "", ErrSecretFileDenied
		}
		return "", fmt.Errorf("cannot read secret file %q: %w", labelPath, unwrapPathError(err))
	}
	if !info.Mode().IsRegular() {
		if policy != nil && policy.Scope == ScopeUpstream {
			return "", ErrSecretFileDenied
		}
		return "", fmt.Errorf("secret file %q is not a regular file", labelPath)
	}
	if policy != nil && policy.deniedByHandleAtRead(info) {
		return "", ErrSecretFileDenied
	}
	return readSecretFromFile(f, labelPath)
}

func readSecretFromFile(f *os.File, labelPath string) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %q: %w", labelPath, unwrapPathError(err))
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %q is not a regular file", labelPath)
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxSecretFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %q: %w", labelPath, unwrapPathError(err))
	}
	if len(data) > MaxSecretFileBytes {
		return "", fmt.Errorf("secret file %q is larger than %d bytes", labelPath, MaxSecretFileBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("secret file %q contains a NUL byte", labelPath)
	}

	switch {
	case bytes.HasSuffix(data, []byte("\r\n")):
		data = data[:len(data)-2]
	case bytes.HasSuffix(data, []byte("\n")):
		data = data[:len(data)-1]
	}
	return string(data), nil
}
