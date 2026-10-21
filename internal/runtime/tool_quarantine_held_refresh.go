package runtime

// RefreshHeldScanEvidence re-runs the in-process TPA scan over the stored
// current definition of every pending or changed tool record of one server and
// refreshes only the held-scan evidence (HeldVerdict, HeldSignals), so a newly
// activated bundle's verdict is visible on tools that are already held for
// review (Spec 101 FR-019 leg (i), tasks B17). It takes the tool-approval lock
// and works for quarantined servers too.
//
// It is evidence only: Status, ApprovedHash, ApprovedBy and the server's
// quarantine flag are never written, and nothing is approved or unquarantined
// (RT-02). A signature removed by a newer bundle leaves the tool held, with
// verdict clean and reason awaiting_review.
//
// STUB (Spec 101 US3 wave 0): returns nil without touching anything until
// T367.
func (r *Runtime) RefreshHeldScanEvidence(server string) error {
	_ = server
	return nil
}
