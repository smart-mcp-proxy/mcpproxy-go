// Package commitview holds the value types that describe one committed view of
// a server (Spec 116). They are shared by configsvc, storage and upstream,
// which must never import each other, so this package imports only the
// standard library (review R7.2).
package commitview

import (
	"errors"
	"strconv"
)

// ServerGen identifies the committed configuration of one server.
//
// Gen is the commit generation at which the server's entry was created or
// last changed; an unchanged entry keeps it across unrelated commits.
// Incarnation is the generation at which the name was last created; it is
// kept through updates and changes only on a remove followed by a create.
//
// OAuthEpoch (Spec 116 PR2b, #1607) is the generation of the commit that
// created the name or last cleared its OAuth state because its OAuth
// configuration changed. 0 on a row written before PR2b; such a row keeps 0
// until its next create or OAuth change.
type ServerGen struct {
	Gen         int64 `json:"gen"`
	Incarnation int64 `json:"incarnation"`
	OAuthEpoch  int64 `json:"oauth_epoch,omitempty"`
}

// IsZero reports whether g carries no stamp (a record written before Spec 116).
func (g ServerGen) IsZero() bool { return g.Gen == 0 && g.Incarnation == 0 }

// Binding is the credential binding of this committed view: the identity
// every OAuth token, client registration and completion record an instance
// built for g writes, reads and clears is bound to (data-model §3).
func (g ServerGen) Binding() CredentialBinding {
	return CredentialBinding{Incarnation: g.Incarnation, OAuthEpoch: g.OAuthEpoch}
}

// CredentialBinding binds OAuth state to one server incarnation and one OAuth
// configuration of it (Spec 116 PR2b, #1607, review R5.3). An OAuth config
// change that keeps the name and URL keeps the incarnation but moves the
// epoch; a remove and re-add moves both.
//
// The zero value is "unbound": a caller with no committed view (a CLI-only
// client, a test fixture). Storage treats unbound callers as the current
// state of the name (see storage.oauthWriteAllowedTx).
type CredentialBinding struct {
	Incarnation int64 `json:"incarnation"`
	OAuthEpoch  int64 `json:"oauth_epoch"`
}

// IsZero reports whether b is the unbound binding.
func (b CredentialBinding) IsZero() bool { return b.Incarnation == 0 && b.OAuthEpoch == 0 }

// String formats b for logs ("inc=I epoch=E").
func (b CredentialBinding) String() string {
	return "inc=" + strconv.FormatInt(b.Incarnation, 10) + " epoch=" + strconv.FormatInt(b.OAuthEpoch, 10)
}

// ErrStaleGeneration reports that an action was bound to a generation that is
// no longer the committed one. It is internal: callers drop the action and
// never surface the error.
var ErrStaleGeneration = errors.New("stale config generation")

// ErrServerReplaced reports that the server a caller bound to (by generation
// or incarnation) was removed or replaced before its write ran. It lives here
// so the scanner and the HTTP layer, which must not import the runtime, can
// match it (Spec 116 PR2). It surfaces as REST 409 "server_replaced".
var ErrServerReplaced = errors.New("server_replaced")

// ServerReplacedError is the ErrServerReplaced error for one server whose
// incarnation moved. Its text is the REST 409 contract:
// "server_replaced: server '<name>' was removed or replaced since it was reviewed".
type ServerReplacedError struct{ Name string }

func (e *ServerReplacedError) Error() string {
	return "server_replaced: server '" + e.Name + "' was removed or replaced since it was reviewed"
}

// Is makes errors.Is(err, ErrServerReplaced) match.
func (e *ServerReplacedError) Is(target error) bool { return target == ErrServerReplaced }
