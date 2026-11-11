package oauth

import (
	"errors"
	"fmt"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/tokenseal"
)

// Spec 123 FR-031: a stored OAuth row the configured key cannot open is not
// "no token". Every read in this package and in the upstream layer turns the
// storage error into TokenUndecryptableError, which carries the diagnostics
// code MCPX_OAUTH_TOKEN_UNDECRYPTABLE and the canonical detail sentence the
// health calculator shows ("stored OAuth token cannot be decrypted: <reason>").
// Nothing that sees it may start an interactive login: a login could not
// overwrite the row (storage FR-013).

// IsTokenUndecryptable reports whether err is (or wraps) the storage error
// for an undecryptable oauth_tokens row.
func IsTokenUndecryptable(err error) bool {
	return errors.Is(err, storage.ErrOAuthTokenUndecryptableSentinel)
}

// TokenUndecryptableDetail is the health detail sentence for an undecryptable
// row: "stored OAuth token cannot be decrypted: <reason in words>". It names
// the reason only, never key material or the key reference.
func TokenUndecryptableDetail(err error) string {
	var ue *storage.ErrOAuthTokenUndecryptable
	var reason tokenseal.Reason
	if errors.As(err, &ue) {
		reason = ue.Reason
	}
	return "stored OAuth token cannot be decrypted: " + undecryptableReasonText(reason)
}

// UndecryptableReasonText is the words for a tokenseal reason code
// ("key_mismatch" -> "sealed with a different key"), as the health detail and
// the login explanation phrase it.
func UndecryptableReasonText(reason string) string {
	return undecryptableReasonText(tokenseal.Reason(reason))
}

func undecryptableReasonText(r tokenseal.Reason) string {
	switch r {
	case tokenseal.ReasonNoKey:
		return "no encryption key configured"
	case tokenseal.ReasonKeyMismatch:
		return "sealed with a different key"
	case tokenseal.ReasonAuthFailed:
		return "failed authentication"
	case tokenseal.ReasonUnsupportedVersion:
		return "unsupported envelope version"
	case tokenseal.ReasonMalformed:
		return "not a valid stored token"
	case "":
		return "unknown reason"
	default:
		return string(r)
	}
}

// tokenUndecryptableError is the message wrapper of TokenUndecryptableError.
type tokenUndecryptableError struct {
	msg string
	err error
}

func (e *tokenUndecryptableError) Error() string { return e.msg + " (" + e.err.Error() + ")" }
func (e *tokenUndecryptableError) Unwrap() error { return e.err }

// TokenUndecryptableError wraps the storage error for serverName's
// undecryptable row with the detail sentence, the two ways out, and the
// MCPX_OAUTH_TOKEN_UNDECRYPTABLE code. errors.Is(…, storage.
// ErrOAuthTokenUndecryptableSentinel) still holds. An error that already
// carries the code is returned unchanged; nil stays nil.
func TokenUndecryptableError(serverName string, err error) error {
	if err == nil {
		return nil
	}
	var coded interface{ Code() diagnostics.Code }
	if errors.As(err, &coded) && coded.Code() == diagnostics.OAuthTokenUndecryptable {
		return err
	}
	msg := fmt.Sprintf("%s; restore the encryption key and restart, or run `mcpproxy auth logout --server=%s` and then log in again",
		TokenUndecryptableDetail(err), serverName)
	return diagnostics.WrapError(diagnostics.OAuthTokenUndecryptable, &tokenUndecryptableError{msg: msg, err: err})
}
