package telemetry

// TPABundleSnapshot is the per-heartbeat state of the ACTIVE TPA signature
// bundle (Spec 101 FR-021, research D7). It is STATE, not a counter: it is read
// fresh on every heartbeat build and is never cleared by the reset-on-accept
// counter window.
//
// PROVENANCE GATE: reporting the schema line and the sequence only when the
// active bundle is the embedded default or was signature-verified against a
// PUBLISHER key (and reporting "other" with Sequence nil otherwise, whatever
// version string the bundle claims) is the PROVIDER's job. Only the scanner
// knows which authority verified a bundle; this package must not import it. The
// anonymity backstop (scanTPABundle) independently enforces the whole tuple, so
// a provider bug fails closed (the heartbeat is refused) instead of leaking.
type TPABundleSnapshot struct {
	// Source is the closed enum "embedded" | "file" | "fetch".
	Source string
	// SignatureVerified is whether the active bundle verified against a trusted
	// key. It is a plain boolean on the wire.
	SignatureVerified bool
	// BundleVersion is "other" or a schema line "<major>.<minor>" (for example
	// "0.1"). Never the bundle's free-form release string.
	BundleVersion string
	// Sequence is the monotonic publisher sequence; non-nil ONLY together with a
	// schema-line BundleVersion.
	Sequence *uint64
}

// SetTPABundleProvider wires a function that returns the active-bundle
// snapshot for the next heartbeat. Each call must return fresh data. Returning
// nil adds nothing to the payload.
func (s *Service) SetTPABundleProvider(fn func() *TPABundleSnapshot) {
	s.tpaBundleProvider = fn
}
