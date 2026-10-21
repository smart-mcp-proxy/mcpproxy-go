package telemetry

// TPABundleRefreshClass is the coarse class of one TPA bundle refresh cycle
// (Spec 087 / Spec 101 US3 tasks B21). It is a telemetry-local string enum
// because this package must not import internal/security/scanner (see
// tpa_bundle.go); internal/server maps scanner.RefreshOutcomeClass to it.
type TPABundleRefreshClass string

const (
	// TPABundleRefreshActivated: the cycle activated a bundle.
	TPABundleRefreshActivated TPABundleRefreshClass = "activated"
	// TPABundleRefreshRejected: the pipeline refused a candidate.
	TPABundleRefreshRejected TPABundleRefreshClass = "rejected"
	// TPABundleRefreshUnavailable: the fetch was unavailable or the channel was
	// malformed.
	TPABundleRefreshUnavailable TPABundleRefreshClass = "unavailable"
	// TPABundleRefreshUnchanged: nothing changed (unchanged, not modified,
	// superseded or disabled).
	TPABundleRefreshUnchanged TPABundleRefreshClass = "unchanged"
)

// RecordTPABundleRefreshOn counts one refresh cycle of the given class on reg if
// reg is non-nil (schema v16): bundle_refresh_cycles always, plus the
// activations, rejections or unavailable counter for the matching class.
// A cycle of class unchanged, or of a class this build does not know, counts as
// a cycle alone: the class string is never transmitted.
func RecordTPABundleRefreshOn(reg *CounterRegistry, class TPABundleRefreshClass) {
	if reg == nil {
		return
	}
	reg.recordTPABundleRefresh(class)
}

// RecordTPABundleReprotectHitOn counts one server whose re-protection scan had
// at least one hard finding (bundle_reprotect_hits) on reg if reg is non-nil
// (schema v16).
func RecordTPABundleReprotectHitOn(reg *CounterRegistry) {
	if reg == nil {
		return
	}
	reg.recordTPABundleReprotectHit()
}
