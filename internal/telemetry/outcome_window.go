package telemetry

import (
	"time"

	"go.etcd.io/bbolt"
	"go.uber.org/zap"
)

// Spec 126 FR-036: the coverage of upstream_outcomes, and (window_age) the
// install age at which each window opened.
//
// A persisted "undelivered window" marker in the activation bucket says
// whether the previous process left recorded outcomes that never reached an
// accepted send:
//
//   - set (true) by the first outcome recorded after process start or after
//     each Drain (a counter window's first outcome);
//   - cleared (false) on an accepted send — the graceful-shutdown flush
//     included — only when no outcome was recorded since the Drain whose
//     snapshot was accepted. sendHeartbeatWithOneShots drains at the START
//     of a send, so outcomes recorded while it (or its retries) are in flight
//     are still undelivered when it is accepted;
//   - absent only on a store no schema-17 build has run on yet. Every start
//     writes it as false when absent, after deciding the first window.
//
// At process start the first window is "partial" when the marker is set, or
// when it is absent and install_origin is "existing" (once). A fresh origin
// with an absent marker (a crash before the start-time write) is "full", as
// are a plain restart after a delivered window and every window after an
// accepted send with nothing recorded since its drain. Both marker writes run
// under the outcome window's own small mutex (outcomeWindow in
// upstream_outcomes.go), together with the outcome increment and Drain's
// swap, so a stale clear can never overwrite a set. deliveryMu is NOT used:
// it is held across the HTTP POST, and a tool call must never wait on
// network I/O.
//
// Each window also opens at a time — registry creation (process start) or the
// last Drain — and window_age is the install age at that time, frozen into
// the drained snapshot.

// activationKeyOutcomeWindowUndelivered is the marker's key in the activation
// bucket: one byte, 0x01 set, 0x00 cleared.
const activationKeyOutcomeWindowUndelivered = "outcome_window_undelivered"

// loadOutcomeWindowMarker returns the marker and whether it exists.
func loadOutcomeWindowMarker(db *bbolt.DB) (set, exists bool, err error) {
	err = db.View(func(tx *bbolt.Tx) error {
		b := activationBucket(tx)
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(activationKeyOutcomeWindowUndelivered))
		if raw == nil {
			return nil
		}
		exists = true
		set = decodeBool(raw)
		return nil
	})
	return set, exists, err
}

func storeOutcomeWindowMarker(db *bbolt.DB, set bool) error {
	return db.Update(func(tx *bbolt.Tx) error {
		b, err := activationBucketForWrite(tx)
		if err != nil {
			return err
		}
		return b.Put([]byte(activationKeyOutcomeWindowUndelivered), encodeBool(set))
	})
}

// InitOutcomeWindow sets up the first counter window of this process. The
// runtime calls it once at startup, after SetActivationStore and
// SetInstallAnchorProvider (and after storage recorded the install anchor).
// storeWasEmpty is true when config.db held no prior data when this process
// opened it.
//
// It installs the window_age mapping (from the once-per-process anchor read),
// decides the first window's coverage from the persisted marker, writes the
// marker as cleared when absent, and installs the marker writer, which the
// outcome window then drives under its own mutex. Without an activation DB no
// marker can be kept, so coverage stays full.
func (s *Service) InitOutcomeWindow(storeWasEmpty bool) {
	reg := s.registry
	if reg == nil {
		return
	}
	if fn := s.outcomeWindowAgeFunc(); fn != nil {
		reg.SetOutcomeWindowAgeFunc(fn)
	}
	db := s.activationDB
	if db == nil {
		return
	}

	set, exists, err := loadOutcomeWindowMarker(db)
	switch {
	case err != nil:
		// Unreadable marker: the predecessor's delivery is unknown, so the
		// window is not claimed to be complete (a store that was empty at
		// open has no predecessor).
		s.logger.Debug("telemetry: outcome window marker unreadable; first window partial", zap.Error(err))
		if !storeWasEmpty {
			reg.MarkOutcomeWindowPartial()
		}
	case !exists:
		// Absent: no schema-17 build has run on this store yet — partial
		// once, but only when install_origin is "existing" (an upgrade,
		// whose older build's unsent counts are unknown). With an anchor
		// (install_origin "fresh") an absent marker can only mean this
		// install crashed before its first start-time write: full.
		if !s.installOriginFresh() {
			reg.MarkOutcomeWindowPartial()
		}
		s.writeOutcomeMarker(false)
	case set:
		reg.MarkOutcomeWindowPartial()
	}

	reg.SetOutcomeMarkerWriter(s.writeOutcomeMarker)
}

// writeOutcomeMarker persists the undelivered-window marker. The outcome
// window calls it under its own mutex (see outcomeWindow); errors are logged
// only: a lost write can only misreport coverage, never block a call.
func (s *Service) writeOutcomeMarker(set bool) {
	db := s.activationDB
	if db == nil {
		return
	}
	if err := storeOutcomeWindowMarker(db, set); err != nil {
		s.logger.Debug("telemetry: failed to write outcome window marker", zap.Bool("set", set), zap.Error(err))
	}
}

// onCounterWindowAccepted runs after a 2xx (the shutdown flush included): it
// clears the undelivered marker unless an outcome was recorded since the
// accepted snapshot's drain.
func (s *Service) onCounterWindowAccepted() {
	s.registry.ClearOutcomeMarkerIfIdle()
}

// installOriginFresh reports whether day_two would say install_origin
// "fresh": the one anchor read of this process found an anchor.
func (s *Service) installOriginFresh() bool {
	_, ok, err, wired := s.installAnchor()
	return wired && err == nil && ok
}

// outcomeWindowAgeFunc returns the window_age mapping, or nil when window_age
// must be omitted: the server edition, no anchor provider, an existing
// install (no anchor) or an unreadable anchor.
func (s *Service) outcomeWindowAgeFunc() func(time.Time) string {
	if s.edition == editionServer {
		return nil
	}
	anchor, ok, err, wired := s.installAnchor()
	if !wired || err != nil || !ok {
		return nil
	}
	return func(opened time.Time) string { return InstallAgeBucket(anchor, opened) }
}
