package scanner

import "time"

// BundleDoctorWarnings turns the bundle status into at most three short doctor
// warnings: stale, stuck on last-known-good, and fetch configured but inactive
// (tasks B16). The strings carry no digest, path, URL or host. It is a pure
// function of its arguments so the truth table is testable without a runtime.
//
// STUB (Spec 101 US3 wave 0): returns nil until T375.
func BundleDoctorWarnings(info BundleInfo, now time.Time) []string {
	_, _ = info, now
	return nil
}
