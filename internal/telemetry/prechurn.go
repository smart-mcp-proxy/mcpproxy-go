package telemetry

import (
	"regexp"

	"go.etcd.io/bbolt"
)

// PreChurnBucketName is the BBolt bucket that stores the Spec 080 US3
// pre-churn snapshot state: the armed/resolved shutdown marker behind
// previous_shutdown, and the single most-recent MCPX_* code behind
// last_error_code (FR-010..FR-013). Both persisted values are fixed-enum
// strings — never free text, paths, or server names.
const PreChurnBucketName = "telemetry_prechurn"

// Fixed keys inside the pre-churn bucket.
const (
	// prechurnKeyShutdownMarker holds "armed" (a process instance started and
	// has not yet completed the graceful-shutdown path) or "clean" (the last
	// instance resolved the marker on graceful shutdown). Absent before the
	// first run ever.
	prechurnKeyShutdownMarker = "shutdown_marker"
	// prechurnKeyLastErrorCode holds the single most recently observed stable
	// MCPX_* diagnostic code. Absent when no error was ever recorded (FR-012).
	prechurnKeyLastErrorCode = "last_error_code"
)

// On-disk marker states. Deliberately distinct from the wire enum below: the
// marker records what THIS instance's lifecycle did; the wire value is what
// the NEXT instance derives from it.
const (
	shutdownMarkerArmed = "armed"
	shutdownMarkerClean = "clean"
)

// Wire enum for the heartbeat previous_shutdown field (FR-010).
const (
	// PreviousShutdownClean: the previous instance completed the graceful
	// shutdown path.
	PreviousShutdownClean = "clean"
	// PreviousShutdownCrash: the previous instance armed the marker but never
	// resolved it (SIGKILL, panic, power loss).
	PreviousShutdownCrash = "crash"
	// PreviousShutdownUnknown: no prior marker exists (first-ever run). Empty
	// so omitempty drops the field — a fresh install is never misreported as
	// a crash (FR-013).
	PreviousShutdownUnknown = ""
)

// mcpxCodePattern is the only shape RecordLastErrorCode will persist and
// LastErrorCode will surface: a stable MCPX_* enum constant. Anything else
// (free text, messages, paths) is silently dropped so PII can never reach
// the telemetry pipeline through this field (FR-012).
var mcpxCodePattern = regexp.MustCompile(`^MCPX_[A-Z0-9_]+$`)

// maxLastErrorCodeLen bounds the persisted code length as defense in depth;
// real MCPX_* constants are far shorter.
const maxLastErrorCodeLen = 64

// PreChurnStore is the persistence contract for the pre-churn snapshot
// (Spec 080 US3). The BBolt implementation uses transactional updates so
// individual calls are atomic. Single-writer safety comes from the BBolt
// file lock: a second process instance fails to open the DB (exit code 3)
// and therefore can never touch the marker (FR-013).
type PreChurnStore interface {
	// ArmShutdownMarker derives the previous instance's shutdown outcome from
	// the persisted marker — PreviousShutdownClean (marker resolved),
	// PreviousShutdownCrash (marker armed but unresolved), or
	// PreviousShutdownUnknown (no marker; first-ever run) — and re-arms the
	// marker for the current instance in the same transaction. Call once,
	// early in startup, so crash loops are visible (FR-010 edge case).
	ArmShutdownMarker(db *bbolt.DB) (string, error)

	// ResolveCleanShutdown marks the current instance's shutdown as graceful.
	// Called from the runtime shutdown path while the DB is still open.
	// Idempotent.
	ResolveCleanShutdown(db *bbolt.DB) error

	// RecordLastErrorCode persists code as the most recent diagnostic code,
	// overwriting any prior value. Only stable MCPX_* enum shapes are
	// accepted; anything else is silently dropped (FR-012).
	RecordLastErrorCode(db *bbolt.DB, code string) error

	// LastErrorCode returns the most recently recorded MCPX_* code, or ""
	// when none was ever recorded. Values are re-validated at read time so a
	// corrupt on-disk value is never surfaced.
	LastErrorCode(db *bbolt.DB) (string, error)
}

// bboltPreChurnStore is the BBolt-backed PreChurnStore implementation.
// Zero-value is ready to use.
type bboltPreChurnStore struct{}

// NewPreChurnStore returns a BBolt-backed PreChurnStore.
func NewPreChurnStore() PreChurnStore {
	return bboltPreChurnStore{}
}

// isValidMCPXCode reports whether code is a transmissible MCPX_* enum value.
func isValidMCPXCode(code string) bool {
	return len(code) <= maxLastErrorCodeLen && mcpxCodePattern.MatchString(code)
}

func (bboltPreChurnStore) ArmShutdownMarker(db *bbolt.DB) (string, error) {
	previous := PreviousShutdownUnknown
	err := db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreChurnBucketName))
		if err != nil {
			return err
		}
		switch string(b.Get([]byte(prechurnKeyShutdownMarker))) {
		case shutdownMarkerClean:
			previous = PreviousShutdownClean
		case shutdownMarkerArmed:
			previous = PreviousShutdownCrash
		default:
			// No marker (first-ever run) or an unrecognized value from a
			// future schema: report unknown, never crash (FR-013).
			previous = PreviousShutdownUnknown
		}
		return b.Put([]byte(prechurnKeyShutdownMarker), []byte(shutdownMarkerArmed))
	})
	if err != nil {
		return PreviousShutdownUnknown, err
	}
	return previous, nil
}

func (bboltPreChurnStore) ResolveCleanShutdown(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreChurnBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(prechurnKeyShutdownMarker), []byte(shutdownMarkerClean))
	})
}

func (bboltPreChurnStore) RecordLastErrorCode(db *bbolt.DB, code string) error {
	if !isValidMCPXCode(code) {
		return nil
	}
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(PreChurnBucketName))
		if err != nil {
			return err
		}
		return b.Put([]byte(prechurnKeyLastErrorCode), []byte(code))
	})
}

func (bboltPreChurnStore) LastErrorCode(db *bbolt.DB) (string, error) {
	var code string
	err := db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(PreChurnBucketName))
		if b == nil {
			return nil
		}
		code = string(b.Get([]byte(prechurnKeyLastErrorCode)))
		return nil
	})
	if err != nil {
		return "", err
	}
	if !isValidMCPXCode(code) {
		return "", nil
	}
	return code, nil
}
