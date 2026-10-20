package secret

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// MaxSecretFileBytes caps how much of a secret file is read. A token, a
// password or an API key is a few hundred bytes; a PEM key is a few KiB. The
// cap keeps a mis-pointed path (a log, a device, a data file) from being read
// into memory and into a process's environment.
const MaxSecretFileBytes = 64 * 1024

// ReadFile reads a secret from an absolute file path, the way Docker Compose
// and Swarm secrets (/run/secrets/<name>), Kubernetes secret volumes and
// systemd LoadCredential= (/run/credentials/<unit>/<name>) deliver one.
//
// Rules, shared by every caller so they behave the same:
//   - the path must be absolute: a relative path would resolve against a
//     working directory the operator did not choose;
//   - symlinks are followed (Kubernetes secret volumes are symlinks), but the
//     target must be a regular file;
//   - at most MaxSecretFileBytes are accepted; a larger file is an error, not a
//     truncated secret;
//   - exactly one trailing newline (LF or CRLF) is removed, because most
//     editors and `echo` add one and no secret should end in it; every other
//     byte, including leading and inner whitespace, is kept;
//   - a NUL byte is rejected, since the value ends up in an HTTP header or an
//     environment variable, neither of which can carry one.
//
// An empty result is returned as "" with no error, so a caller can decide
// whether empty is acceptable for its field. Errors name the path and the
// reason and never include any of the file's content.
func ReadFile(path string) (string, error) {
	if path == "" {
		return "", errors.New("secret file path is empty")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("secret file path %q must be absolute", path)
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %q: %w", path, unwrapPathError(err))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %q: %w", path, unwrapPathError(err))
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("secret file %q is not a regular file", path)
	}

	// Read one byte past the cap so an oversized file is detected from what
	// was read, not from a size that may have changed since Stat.
	data, err := io.ReadAll(io.LimitReader(f, MaxSecretFileBytes+1))
	if err != nil {
		return "", fmt.Errorf("cannot read secret file %q: %w", path, unwrapPathError(err))
	}
	if len(data) > MaxSecretFileBytes {
		return "", fmt.Errorf("secret file %q is larger than %d bytes", path, MaxSecretFileBytes)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("secret file %q contains a NUL byte", path)
	}

	switch {
	case bytes.HasSuffix(data, []byte("\r\n")):
		data = data[:len(data)-2]
	case bytes.HasSuffix(data, []byte("\n")):
		data = data[:len(data)-1]
	}
	return string(data), nil
}

// unwrapPathError drops the "open <path>:" prefix of an *os.PathError so the
// path is named once, by the caller's own message.
func unwrapPathError(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}
