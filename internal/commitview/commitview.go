// Package commitview holds the value types that describe one committed view of
// a server (Spec 116). They are shared by configsvc, storage and upstream,
// which must never import each other, so this package imports only the
// standard library (review R7.2).
package commitview

import "errors"

// ServerGen identifies the committed configuration of one server.
//
// Gen is the commit generation at which the server's entry was created or
// last changed; an unchanged entry keeps it across unrelated commits.
// Incarnation is the generation at which the name was last created; it is
// kept through updates and changes only on a remove followed by a create.
type ServerGen struct {
	Gen         int64 `json:"gen"`
	Incarnation int64 `json:"incarnation"`
}

// IsZero reports whether g carries no stamp (a record written before Spec 116).
func (g ServerGen) IsZero() bool { return g.Gen == 0 && g.Incarnation == 0 }

// ErrStaleGeneration reports that an action was bound to a generation that is
// no longer the committed one. It is internal: callers drop the action and
// never surface the error.
var ErrStaleGeneration = errors.New("stale config generation")
