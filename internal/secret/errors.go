package secret

import "errors"

// ErrSecretFileDenied is returned when a ${file:} reference is refused by
// secret_file_dirs, a deny path, or a disabled file provider. The message is
// fixed and must not echo the path or any file content.
var ErrSecretFileDenied = errors.New("secret file is missing, unreadable or outside secret_file_dirs")

// ErrReservedEnvVar is returned when an upstream-scoped resolver refuses a
// ${env:MCPROXY_…} reference. The message is fixed and must not echo the name.
var ErrReservedEnvVar = errors.New("secret reference refused: this environment variable is reserved")
