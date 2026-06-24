//go:build !linux

package sandbox

// Apply is a no-op stub on non-Linux platforms. Landlock is a Linux-only LSM,
// so there is no equivalent unprivileged filesystem-allowlist primitive here.
// With BestEffort the caller may proceed unconfined (recorded in the Report);
// otherwise Apply fails closed with ErrUnsupported.
//
// Note: macOS/Windows already have their own first-class sandbox stories
// (Seatbelt / Docker Desktop / Windows containers); this package targets the
// Linux snap-docker gap specifically (see package doc).
func Apply(spec Spec) (Report, error) {
	// No filesystem allowlist requested → nothing to enforce, same as Linux.
	if !spec.wantsLandlock() {
		return Report{LandlockNote: "no filesystem allowlist requested; Landlock is Linux-only"}, nil
	}
	rep := Report{LandlockABI: -1, LandlockNote: "Landlock is Linux-only"}
	if !spec.BestEffort {
		return rep, ErrUnsupported
	}
	return rep, nil
}
