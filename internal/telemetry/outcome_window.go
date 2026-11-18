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
// accepted send with nothing recorded since its drain. The marker writes are
// serialised by outcomeMarkerMu, which the accept path holds across its
// "anything recorded since the drain?" check and its write, so a stale clear
// can never overwrite a set. (deliveryMu is not used for the set: it is held
// across the HTTP request, and the set runs on the tool-call path.)
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
// SetInstallAnchorProvider. storeWasEmpty is true when config.db held no prior
// data when this process opened it (a fresh install).
//
// It installs the window_age mapping (the anchor is constant for the process:
// it is written at startup before telemetry is wired, and never rewritten),
// decides the first window's coverage from the persisted marker, writes the
// marker as cleared when absent, and installs the hook that sets it when a
// window records its first outcome. Without an activation DB no marker can be
// kept, so coverage stays full.
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

	s.outcomeMarkerMu.Lock()
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
		if err := storeOutcomeWindowMarker(db, false); err != nil {
			s.logger.Debug("telemetry: failed to initialise outcome window marker", zap.Error(err))
		}
	case set:
		reg.MarkOutcomeWindowPartial()
	}
	s.outcomeMarkerMu.Unlock()

	reg.SetUndeliveredWindowHook(s.markOutcomeWindowUndelivered)
}

// markOutcomeWindowUndelivered persists the marker for the first outcome of
// the window with drain epoch epoch. A hook that lands after that window was
// already accepted (the recording goroutine was preempted between counting
// and calling the hook) writes nothing, so it cannot leave a false "partial".
// Serialised with the clear on an accepted send.
func (s *Service) markOutcomeWindowUndelivered(epoch uint64) {
	db := s.activationDB
	if db == nil {
		return
	}
	s.outcomeMarkerMu.Lock()
	defer s.outcomeMarkerMu.Unlock()
	if s.outcomeDelivered && epoch <= s.outcomeDeliveredEpoch {
		return
	}
	if err := storeOutcomeWindowMarker(db, true); err != nil {
		s.logger.Debug("telemetry: failed to set outcome window marker", zap.Error(err))
	}
}

// onCounterWindowAccepted runs after a 2xx (the shutdown flush included) for
// the drained snapshot of epoch acceptedEpoch. It clears the undelivered
// marker unless an outcome was recorded since that drain (its own hook set
// the marker, and those outcomes are still undelivered).
func (s *Service) onCounterWindowAccepted(acceptedEpoch uint64) {
	reg := s.registry
	db := s.activationDB
	if reg == nil || db == nil {
		return
	}
	s.outcomeMarkerMu.Lock()
	defer s.outcomeMarkerMu.Unlock()
	s.outcomeDelivered = true
	s.outcomeDeliveredEpoch = acceptedEpoch
	if reg.HasPendingUpstreamOutcomes() {
		return
	}
	if err := storeOutcomeWindowMarker(db, false); err != nil {
		s.logger.Debug("telemetry: failed to clear outcome window marker", zap.Error(err))
	}
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
