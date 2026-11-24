package secret

import (
	"errors"
	"os"
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
// ReadFile reads a secret without secret_file_dirs constraints. Callers that
// enforce policy (the file provider, or MCPPROXY_API_KEY_FILE after separate
// admin-path checks) use ReadFileWithPolicy instead.
func ReadFile(path string) (string, error) {
	return ReadFileWithPolicy(path, NewWildcardFilePolicy(ScopeProxy))
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
