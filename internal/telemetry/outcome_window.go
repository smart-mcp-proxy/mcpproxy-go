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
//   - set (true) when a window records its first outcome;
//   - cleared (false) on an accepted send — the graceful-shutdown flush
//     included — unless the next window already holds an outcome;
//   - absent on a store this build never ran on.
//
// At process start the first window is "partial" when the marker is set, or
// absent on an existing store (the first start of this build, once: the
// marker is then written as false). A fresh install, and every window after
// the first accepted send, is "full". A plain restart after a delivered
// window is therefore "full".

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
// It decides the first window's coverage from the persisted marker, sets the
// first window's age, and installs the hook that sets the marker when a
// window records its first outcome. Without an activation DB it only sets the
// window age (no marker can be kept, so coverage stays full).
func (s *Service) InitOutcomeWindow(storeWasEmpty bool) {
	reg := s.registry
	if reg == nil {
		return
	}
	reg.SetOutcomeWindowAge(s.outcomeWindowAge(time.Now().UTC()))
	db := s.activationDB
	if db == nil {
		return
	}

	s.outcomeMarkerMu.Lock()
	set, exists, err := loadOutcomeWindowMarker(db)
	switch {
	case err != nil:
		// Unreadable marker: the predecessor's delivery is unknown, so the
		// window is not claimed to be complete.
		s.logger.Debug("telemetry: outcome window marker unreadable; first window partial", zap.Error(err))
		if !storeWasEmpty {
			reg.MarkOutcomeWindowPartial()
		}
	case !exists:
		if !storeWasEmpty {
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

// markOutcomeWindowUndelivered persists the marker when a window records its
// first outcome. Serialised with the clear on an accepted send.
func (s *Service) markOutcomeWindowUndelivered() {
	db := s.activationDB
	if db == nil {
		return
	}
	s.outcomeMarkerMu.Lock()
	defer s.outcomeMarkerMu.Unlock()
	if err := storeOutcomeWindowMarker(db, true); err != nil {
		s.logger.Debug("telemetry: failed to set outcome window marker", zap.Error(err))
	}
}

// onCounterWindowAccepted runs after a 2xx (the shutdown flush included). It
// opens the next window — its age is the install age now — and clears the
// undelivered marker unless the next window already recorded an outcome
// (recorded between the drain and this accept; its own hook set the marker).
func (s *Service) onCounterWindowAccepted() {
	reg := s.registry
	if reg == nil {
		return
	}
	reg.SetOutcomeWindowAge(s.outcomeWindowAge(time.Now().UTC()))
	db := s.activationDB
	if db == nil {
		return
	}
	s.outcomeMarkerMu.Lock()
	defer s.outcomeMarkerMu.Unlock()
	if reg.HasPendingUpstreamOutcomes() {
		return
	}
	if err := storeOutcomeWindowMarker(db, false); err != nil {
		s.logger.Debug("telemetry: failed to clear outcome window marker", zap.Error(err))
	}
}

// outcomeWindowAge is the install age bucket for a window opening at now, or
// "" when it must be omitted: the server edition, no anchor provider, an
// existing install (no anchor) or an unreadable anchor.
func (s *Service) outcomeWindowAge(now time.Time) string {
	if s.edition == editionServer || s.installAnchorProvider == nil {
		return ""
	}
	anchor, ok, err := s.installAnchorProvider()
	if err != nil || !ok {
		return ""
	}
	return InstallAgeBucket(anchor, now)
}
