package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/secret"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123: the key that seals upstream OAuth tokens in config.db.

const (
	// OAuthTokenKeyField is the JSON name of Config.OAuthTokenEncryptionKey.
	OAuthTokenKeyField = "oauth_token_encryption_key"
	// EnvOAuthTokenKey carries the key itself and overrides the field.
	EnvOAuthTokenKey = "MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY"
	// EnvOAuthTokenKeyFile names a file that holds the key (the *_FILE
	// convention of Docker/Kubernetes secrets and systemd credentials, read
	// with the ${file:} rules) and overrides the field.
	EnvOAuthTokenKeyFile = "MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE"

	// ExitCodeOAuthTokenKeyError is the exit code of a process that could not
	// resolve the configured key before opening config.db (FR-022). It equals
	// cmd/mcpproxy's ExitCodeConfigError, which package config cannot import.
	ExitCodeOAuthTokenKeyError = 4
)

// The fixed sentences of every resolution failure. None of them quotes the
// reference, the path or any byte of the key (SC-004); the prefix names the
// field and, when the key came from the environment, the variable.
const (
	MsgOAuthTokenKeyUnresolvable = "could not resolve the secret reference; check that the referenced file, environment variable or keyring entry exists and is readable"
	MsgOAuthTokenKeyEmpty        = "resolved to an empty value"
	// MsgOAuthTokenKeyUnusable is for a file source that exists but cannot be
	// used as one (too large, not a regular file, invalid bytes, relative
	// path). It is a mistake in a present source, never a lost key.
	MsgOAuthTokenKeyUnusable = "is present but is not a usable key source; check that it is an absolute path to a small regular text file"
	// MsgOAuthTokenKeyMalformed is tokenseal.ErrInvalidKey's text.
	MsgOAuthTokenKeyMalformed = "is not a base64-encoded 32-byte key"

	msgOAuthTokenKeyBadReference = "is not a well-formed secret reference; use ${file:/absolute/path}, ${env:NAME}, ${keyring:NAME} or a literal key"
)

// ErrOAuthTokenKeyBothSet is returned when both key variables are non-empty,
// as MCPPROXY_API_KEY and MCPPROXY_API_KEY_FILE are treated.
var ErrOAuthTokenKeyBothSet = NewStartupError(ExitCodeOAuthTokenKeyError,
	EnvOAuthTokenKey+" and "+EnvOAuthTokenKeyFile+" are both set; set only one of them")

// KeySourceKind says where the resolved key came from.
type KeySourceKind string

// Key source kinds. KeySourceNone means encryption is off.
const (
	KeySourceNone       KeySourceKind = ""
	KeySourceLiteral    KeySourceKind = "literal"
	KeySourceEnv        KeySourceKind = "env"
	KeySourceEnvFile    KeySourceKind = "env_file"
	KeySourceRefFile    KeySourceKind = "ref_file"
	KeySourceRefEnv     KeySourceKind = "ref_env"
	KeySourceRefKeyring KeySourceKind = "ref_keyring"
)

// KeySource describes the resolved key for doctor without keeping the key file
// path: only whether that file lies inside data_dir (after following
// symlinks) is recorded.
type KeySource struct {
	Kind      KeySourceKind
	InDataDir bool
}

// ResolveOAuthTokenKey resolves the key that seals oauth_tokens values. It is
// called only where config.db is opened (runtime.New and the CLI open sites),
// once per process; Validate checks the field's syntax only.
//
// Order (env beats the field, like api_key):
//  1. both variables non-empty -> ErrOAuthTokenKeyBothSet;
//  2. MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY_FILE, read with secret.ReadFile;
//  3. MCPPROXY_OAUTH_TOKEN_ENCRYPTION_KEY;
//  4. the field: a ${file:}/${env:}/${keyring:} reference or a literal.
//
// An empty variable counts as unset. Encryption is off (nil key, no error)
// only when no source is selected: both variables empty and the field empty.
// A selected source that resolves to an empty or malformed value is an error,
// never "off": silently writing plaintext over sealed rows is the failure this
// rules out. Every error is a *StartupError with exit code 4 and one of the
// fixed sentences above.
func ResolveOAuthTokenKey(c *Config) (*tokenseal.Key, KeySource, error) {
	envVal := os.Getenv(EnvOAuthTokenKey)
	envFile := os.Getenv(EnvOAuthTokenKeyFile)
	if envVal != "" && envFile != "" {
		return nil, KeySource{}, ErrOAuthTokenKeyBothSet
	}
	field, dataDir := "", ""
	if c != nil {
		field, dataDir = c.OAuthTokenEncryptionKey, c.DataDir
	}

	var (
		raw   string
		src   KeySource
		label = OAuthTokenKeyField
	)
	switch {
	case envFile != "":
		label = OAuthTokenKeyField + " (" + EnvOAuthTokenKeyFile + ")"
		src.Kind = KeySourceEnvFile
		v, err := secret.ReadFile(envFile)
		if err != nil {
			return nil, KeySource{}, oauthTokenKeyError(label, fileSourceSentence(err))
		}
		raw = v
		src.InDataDir = pathWithinDir(envFile, dataDir)
	case envVal != "":
		label = OAuthTokenKeyField + " (" + EnvOAuthTokenKey + ")"
		src.Kind = KeySourceEnv
		raw = envVal
	case field == "":
		return nil, KeySource{}, nil
	case strings.Contains(field, "${"):
		ref, ok := parseOAuthTokenKeyRef(strings.TrimSpace(field))
		if !ok {
			return nil, KeySource{}, oauthTokenKeyError(label, msgOAuthTokenKeyBadReference)
		}
		var (
			v   string
			err error
		)
		switch ref.Type {
		case secret.SecretTypeFile:
			src.Kind = KeySourceRefFile
			// secret.ReadFile directly (the file provider calls the same
			// function) so an empty file reads as "empty", not "unresolvable".
			v, err = secret.ReadFile(ref.Name)
			if err == nil {
				src.InDataDir = pathWithinDir(ref.Name, dataDir)
			} else {
				return nil, KeySource{}, oauthTokenKeyError(label, fileSourceSentence(err))
			}
		case "env":
			src.Kind = KeySourceRefEnv
			// The env provider reports unset and empty alike; only an unset
			// variable is a lost key.
			if ev, set := os.LookupEnv(ref.Name); set && ev == "" {
				return nil, KeySource{}, oauthTokenKeyError(label, MsgOAuthTokenKeyEmpty)
			}
			v, err = secret.NewResolver().Resolve(context.Background(), *ref)
		default: // keyring
			src.Kind = KeySourceRefKeyring
			v, err = secret.NewResolver().Resolve(context.Background(), *ref)
		}
		if err != nil {
			return nil, KeySource{}, oauthTokenKeyError(label, MsgOAuthTokenKeyUnresolvable)
		}
		raw = v
	default:
		src.Kind = KeySourceLiteral
		raw = field
	}

	if strings.TrimSpace(raw) == "" {
		return nil, KeySource{}, oauthTokenKeyError(label, MsgOAuthTokenKeyEmpty)
	}
	key, err := tokenseal.ParseKey(raw)
	if err != nil {
		return nil, KeySource{}, oauthTokenKeyError(label, MsgOAuthTokenKeyMalformed)
	}
	return key, src, nil
}

// IsOAuthTokenKeyUnresolvable reports whether err is ResolveOAuthTokenKey's
// "the reference no longer resolves" error: a source that is missing or
// unreadable (key file absent or permission denied, environment variable not
// set, keyring entry absent): the lost-key case. Every other key error (both
// variables set, a bad reference, an empty, oversized or malformed value) is a
// mistake in a source that is present, and returns false.
func IsOAuthTokenKeyUnresolvable(err error) bool {
	var se *StartupError
	return errors.As(err, &se) && se.ExitCode == ExitCodeOAuthTokenKeyError &&
		strings.HasSuffix(se.Message, ": "+MsgOAuthTokenKeyUnresolvable)
}

// fileSourceSentence classifies a secret.ReadFile failure: a file that does
// not exist or cannot be opened is a lost key; anything else (too large, not a
// regular file, a NUL byte, a relative path) is a present but unusable source.
func fileSourceSentence(err error) string {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.ENOTDIR) {
		return MsgOAuthTokenKeyUnresolvable
	}
	return MsgOAuthTokenKeyUnusable
}

func oauthTokenKeyError(label, sentence string) *StartupError {
	return NewStartupError(ExitCodeOAuthTokenKeyError, label+": "+sentence)
}

// parseOAuthTokenKeyRef accepts exactly one ${file:/abs}, ${env:NAME} or
// ${keyring:NAME} reference spanning the whole value.
func parseOAuthTokenKeyRef(s string) (*secret.Ref, bool) {
	refs := secret.FindSecretRefs(s)
	if len(refs) != 1 || refs[0].Original != s || refs[0].Name == "" {
		return nil, false
	}
	ref := refs[0]
	switch ref.Type {
	case secret.SecretTypeFile:
		if !filepath.IsAbs(ref.Name) {
			return nil, false
		}
	case "env", "keyring":
	default:
		return nil, false
	}
	return ref, true
}

// checkOAuthTokenKeySyntax is the load-time check: empty, a literal (checked
// when the key is resolved), or a well-formed reference. It reads nothing.
func checkOAuthTokenKeySyntax(v string) error {
	s := strings.TrimSpace(v)
	if s == "" || !strings.Contains(s, "${") {
		return nil
	}
	if _, ok := parseOAuthTokenKeyRef(s); !ok {
		return oauthTokenKeyError(OAuthTokenKeyField, msgOAuthTokenKeyBadReference)
	}
	return nil
}

// pathWithinDir reports whether p, with symlinks followed, lies inside dir.
func pathWithinDir(p, dir string) bool {
	if dir == "" {
		return false
	}
	rp, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rd, err := filepath.EvalSymlinks(dir)
	if err != nil {
		rd = filepath.Clean(dir)
	}
	rel, err := filepath.Rel(rd, rp)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
