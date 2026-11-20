package telemetry

import (
	"fmt"

	"go.etcd.io/bbolt"
)

// activationKeySuccessScopeEver holds the lifetime success scope (Spec 126
// FR-038) in the activation bucket as one ordinal byte: 0 none, 1
// starter_only, 2 non_starter.
const activationKeySuccessScopeEver = "success_scope_ever"

// SuccessScope is the lifetime scope of usable upstream results
// (day_two.success_scope_ever). It only moves forward:
// none -> starter_only -> non_starter.
type SuccessScope uint8

// The ordinals are persisted; never renumber them.
const (
	SuccessScopeNone        SuccessScope = 0
	SuccessScopeStarterOnly SuccessScope = 1
	SuccessScopeNonStarter  SuccessScope = 2
)

// String returns the wire value.
func (s SuccessScope) String() string {
	switch s {
	case SuccessScopeNone:
		return "none"
	case SuccessScopeStarterOnly:
		return "starter_only"
	case SuccessScopeNonStarter:
		return "non_starter"
	default:
		return "invalid"
	}
}

func decodeSuccessScope(raw []byte) (SuccessScope, error) {
	if raw == nil {
		return SuccessScopeNone, nil
	}
	if len(raw) != 1 || SuccessScope(raw[0]) > SuccessScopeNonStarter {
		return SuccessScopeNone, fmt.Errorf("activation: invalid %s value", activationKeySuccessScopeEver)
	}
	return SuccessScope(raw[0]), nil
}

// LoadSuccessScope reads the lifetime success scope. A missing bucket or key
// is SuccessScopeNone; a stored value outside the ordinal range is an error.
func LoadSuccessScope(db *bbolt.DB) (SuccessScope, error) {
	if db == nil {
		return SuccessScopeNone, fmt.Errorf("nil db")
	}
	scope := SuccessScopeNone
	err := db.View(func(tx *bbolt.Tx) error {
		b := activationBucket(tx)
		if b == nil {
			return nil
		}
		var err error
		scope, err = decodeSuccessScope(b.Get([]byte(activationKeySuccessScopeEver)))
		return err
	})
	return scope, err
}

// RecordSuccessScope advances the lifetime scope for one usable upstream
// result: starter_only for a starter success, non_starter for any other. It
// never moves the scope back. It is local state, recorded whatever the
// telemetry opt-out setting (only sending is gated). A read-only check runs
// first so the common case (scope already reached) costs no write.
func RecordSuccessScope(db *bbolt.DB, starter bool) error {
	if db == nil {
		return fmt.Errorf("nil db")
	}
	target := SuccessScopeNonStarter
	if starter {
		target = SuccessScopeStarterOnly
	}
	current, err := LoadSuccessScope(db)
	if err != nil {
		return err
	}
	if current >= target {
		return nil
	}
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		cur, err := decodeSuccessScope(b.Get([]byte(activationKeySuccessScopeEver)))
		if err != nil {
			return err
		}
		if cur >= target {
			return nil
		}
		return b.Put([]byte(activationKeySuccessScopeEver), []byte{byte(target)})
	})
}
