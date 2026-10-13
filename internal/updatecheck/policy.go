package updatecheck

// Spec 092 FR-015: the effective update policy as an EXPLICIT contract.
//
// Before this file the only signal a client had was the presence or absence of
// the `update` object in /api/v1/info — and `GetVersionInfo` returns nil both
// when checking is disabled and when a check simply has not produced a result
// yet. A tray that has to decide "may I run a feed check at all?" cannot tell
// those apart, so it either nudges when the operator disabled updates or stays
// silent when it should not. The policy below is always reported, never
// inferred from missing data, and is recomputed on every read so a config
// hot-reload (SetConfig) or an environment override takes effect immediately.

const (
	// PolicyChannelStable offers only stable releases.
	PolicyChannelStable = "stable"
	// PolicyChannelRC also offers prereleases (docs/prerelease-builds.md).
	PolicyChannelRC = "rc"
)

// Policy is the effective, hot-reloadable update policy visible to clients.
type Policy struct {
	// Enabled is the effective kill-switch state: update_check.enabled with
	// MCPPROXY_DISABLE_AUTO_UPDATE=true winning over it. When false, no
	// surface may perform an automatic check. A USER-INITIATED check ("Check
	// for Updates") stays available — this field governs automatic behaviour,
	// which is what FR-015 asks for.
	Enabled bool `json:"enabled"`

	// Channel is the release channel this install tracks: "stable" or "rc".
	Channel string `json:"channel"`

	// NudgesSuppressed is the CI / non-interactive rule (Spec 079 FR-019):
	// machine-readable fields keep reporting the facts, UI surfaces stay
	// quiet.
	NudgesSuppressed bool `json:"nudges_suppressed"`
}

// Policy reports the checker's effective policy. Safe for concurrent use and
// cheap enough to call per request; both environment overrides are read live so
// the answer always matches what the checker itself would do right now.
func (c *Checker) Policy() Policy {
	channel := PolicyChannelStable
	if c.IncludePrereleases() {
		channel = PolicyChannelRC
	}
	c.mu.RLock()
	suppressed := c.nudgesSuppressed
	c.mu.RUnlock()
	return Policy{
		Enabled:          c.Enabled(),
		Channel:          channel,
		NudgesSuppressed: suppressed,
	}
}

// UnavailablePolicy is the policy to report when there is no checker at all
// (server edition, tests, a runtime constructed without one). Automatic checks
// cannot happen, so Enabled is false — reported as a fact rather than left for
// the client to guess. Nudge suppression is still answered from the
// environment, since that rule is about the context, not about the checker.
func UnavailablePolicy() Policy {
	return Policy{
		Enabled:          false,
		Channel:          PolicyChannelStable,
		NudgesSuppressed: isQuietEnvironment(),
	}
}
