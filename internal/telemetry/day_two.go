package telemetry

import (
	"time"

	"go.uber.org/zap"
)

// Schema v17 (Spec 126 FR-034, FR-038): the day_two block, personal edition
// only. STATE read fresh at every heartbeat build, never part of the
// reset-on-accept counter window. It carries closed enums only: the install
// anchor itself (a timestamp) never leaves the machine.

// DayTwoPayload is the wire form of day_two.
type DayTwoPayload struct {
	// InstallOrigin is "fresh" when the store holds an install anchor, and
	// "existing" otherwise (an install upgraded from a build without one).
	InstallOrigin string `json:"install_origin"`
	// InstallAge is the UTC calendar-date distance from the anchor's date to
	// the build date, bucketed; present only when InstallOrigin is "fresh".
	InstallAge string `json:"install_age,omitempty"`
	// SuccessScopeEver is the persisted, forward-only success scope.
	SuccessScopeEver string `json:"success_scope_ever"`
}

// day_two enum values.
const (
	InstallOriginFresh    = "fresh"
	InstallOriginExisting = "existing"

	InstallAgeD0to6   = "d0_6"
	InstallAgeD7to13  = "d7_13"
	InstallAgeD14Plus = "d14_plus"
)

// editionServer is the edition string of the server-edition build
// (cmd/mcpproxy/edition_teams.go). day_two is omitted there.
const editionServer = "server"

// utcDate truncates t to midnight of its UTC calendar date.
func utcDate(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)
}

// InstallAgeBucket returns the install_age bucket for a heartbeat built at
// now: the difference in UTC calendar dates between the anchor's UTC date and
// now's UTC date (so the value is constant within one UTC date). 0–6 is d0_6,
// 7–13 d7_13, 14 and more d14_plus. A negative difference (clock skew, an
// anchor in the future) counts as d0_6.
func InstallAgeBucket(anchor, now time.Time) string {
	// UTC has no DST, so every date is exactly 24h; rounding absorbs nothing
	// but guards against a future change of the truncation.
	days := int(utcDate(now).Sub(utcDate(anchor)).Round(time.Hour).Hours()) / 24
	switch {
	case days < 7:
		return InstallAgeD0to6
	case days < 14:
		return InstallAgeD7to13
	default:
		return InstallAgeD14Plus
	}
}

// BuildDayTwo assembles the day_two block from the install anchor (hasAnchor
// false for an existing install) and the lifetime success scope.
func BuildDayTwo(anchor time.Time, hasAnchor bool, scope SuccessScope, now time.Time) *DayTwoPayload {
	dt := &DayTwoPayload{
		InstallOrigin:    InstallOriginExisting,
		SuccessScopeEver: scope.String(),
	}
	if hasAnchor {
		dt.InstallOrigin = InstallOriginFresh
		dt.InstallAge = InstallAgeBucket(anchor, now)
	}
	return dt
}

// SetInstallAnchorProvider wires the install-anchor reader (Spec 126 FR-033):
// it returns the anchor, whether the store holds one, and a read error. The
// runtime installs it over the storage manager. Without it, or on the server
// edition, the heartbeat omits day_two. Call it before InitOutcomeWindow and
// Start.
func (s *Service) SetInstallAnchorProvider(fn func() (time.Time, bool, error)) {
	s.installAnchorMu.Lock()
	defer s.installAnchorMu.Unlock()
	s.installAnchorProvider = fn
	s.installAnchorRead = false
}

// installAnchor reads the install anchor ONCE per process and returns that
// same answer to every caller: day_two (install_origin, install_age) and
// window_age. The anchor is written at startup before telemetry is wired and
// never rewritten, so one read is exact, and one shared value means the two
// objects can never disagree on "fresh". A disagreement would be fatal: the
// receiver answers 400 to window_age next to install_origin "existing", a
// non-2xx keeps the window pending, and the same rejected window would be
// re-sent forever. A read error is cached too: both objects then omit their
// anchor-derived fields for the whole process. wired is false when no
// provider is installed.
func (s *Service) installAnchor() (anchor time.Time, exists bool, err error, wired bool) {
	s.installAnchorMu.Lock()
	defer s.installAnchorMu.Unlock()
	if s.installAnchorProvider == nil {
		return time.Time{}, false, nil, false
	}
	if !s.installAnchorRead {
		s.installAnchorValue, s.installAnchorExists, s.installAnchorErr = s.installAnchorProvider()
		s.installAnchorRead = true
	}
	return s.installAnchorValue, s.installAnchorExists, s.installAnchorErr, true
}

// buildDayTwo returns the day_two block for this heartbeat, or nil when it
// must be omitted: the server edition, no anchor provider or activation store
// wired, or a read error. A read error omits the block for this heartbeat
// rather than guessing "existing" or "none"; absence reads as "not measured".
func (s *Service) buildDayTwo(now time.Time) *DayTwoPayload {
	if s.edition == editionServer || s.activationDB == nil {
		return nil
	}
	anchor, ok, err, wired := s.installAnchor()
	if !wired {
		return nil
	}
	if err != nil {
		s.logger.Debug("telemetry: install anchor unreadable; omitting day_two", zap.Error(err))
		return nil
	}
	scope, err := LoadSuccessScope(s.activationDB)
	if err != nil {
		s.logger.Debug("telemetry: success scope unreadable; omitting day_two", zap.Error(err))
		return nil
	}
	return BuildDayTwo(anchor, ok, scope, now)
}
