package scanner

// Activation self-check (Spec 087 FR-008, Spec 101 FR-016a(c); tasks B13, B14).
// The real port of the CI gate lands with T343.

// selfCheckCorpus is the test seam that replaces the embedded self-check
// corpus bytes (to feed vacuous or unparsable corpora). nil means the embedded
// corpus.
//
//nolint:unused // referenced by the T340 tests and T343
var selfCheckCorpus func() []byte

// EnableActivationSelfCheck installs the activation self-check as the pipeline's
// bundleSelfCheck hook. Production wiring (internal/server/server.go) calls it
// once, next to configureTPABundleRuntime; US1 fixtures and clearBundleRuntime
// keep the nil default.
//
// STUB (Spec 101 US3 wave 0): no-op, the hook stays nil, until T343.
func EnableActivationSelfCheck() {}

// activationSelfCheck scores a compiled candidate over the embedded self-check
// corpus with the same decision as the CI gate and refuses it on a recall or
// false-positive regression. A non-nil error is reported as gate_failure.
//
// STUB (Spec 101 US3 wave 0): accepts everything until T343.
//
//nolint:unused // installed by EnableActivationSelfCheck once T343 lands
func activationSelfCheck(check *BundleCheck) error {
	_ = check
	return nil
}
