package scanner

// Bundle rejection reasons surfaced in BundleInfo.LastRejection.Reason (spec
// 101 FR-020; contracts/bundle-status-surface.md §2). The Reason is the only
// field anything branches on; the fine-grained cause (the bundlesig contract
// code, which ceiling and by how much, which key id, which epoch) goes in
// Detail. Every rejection keeps last-known-good serving.
const (
	// RejectTamper: the signature did not verify over the raw bytes, or the
	// manifest / sidecar / verifying-key ids disagree (sidecar contract rows 12
	// and 13; SC-001's tamper classes).
	RejectTamper = "tamper"
	// RejectDowngrade: sequence below this authority's watermark for this
	// epoch, or an equal sequence with a different digest.
	RejectDowngrade = "downgrade"
	// RejectStaleEpochReplay: manifest epoch below the current trusted epoch.
	RejectStaleEpochReplay = "stale_epoch_replay"
	// RejectEpochAhead: manifest epoch above the current trusted epoch — this
	// binary (or this config) is older than the publisher's recovery release;
	// update mcpproxy. Does not demote coverage (FR-009a allows two conditions).
	RejectEpochAhead = "epoch_ahead"
	// RejectUnsignedRatchet: an unsigned candidate after a signature-verified
	// bundle has activated on this install (FR-009 ratchet).
	RejectUnsignedRatchet = "unsigned_refused_by_ratchet"
	// RejectSignatureRequired: require_signed_bundle is on and the sidecar is
	// absent.
	RejectSignatureRequired = "signature_required"
	// RejectAuthorityNotActive: verified against a real key of the NON-active
	// root (FR-004b).
	RejectAuthorityNotActive = "authority_not_active"
	// RejectRollbackTargetMismatch: the loaded artifact's full digest is not the
	// digest the operator named (FR-010).
	RejectRollbackTargetMismatch = "rollback_target_mismatch"
	// RejectDenyListed: the candidate is a release the operator rolled away
	// from (FR-010 deny-list).
	RejectDenyListed = "deny_listed"
	// RejectGateFailure: the activation self-check refused the candidate.
	RejectGateFailure = "gate_failure"
	// RejectCeilingExceeded: one of FR-011's limits (bundle 8 MiB, 2000 rules,
	// sidecar 4 KiB) — which one, and the observed value, in Detail.
	RejectCeilingExceeded = "ceiling_exceeded"
	// RejectNotRegularFile: the candidate (or its sidecar) is a FIFO, device,
	// socket, directory, or a symlink.
	RejectNotRegularFile = "not_regular_file"
	// RejectReadTimeout: the per-candidate read budget expired; the
	// single-flight slot was released.
	RejectReadTimeout = "read_timeout"
	// RejectReadError: the candidate could not be opened or read (missing file,
	// permission denied, I/O error).
	RejectReadError = "read_error"
	// RejectSidecarInvalid: the sidecar violates its grammar (bundlesig
	// sidecar_* codes other than sidecar_too_large).
	RejectSidecarInvalid = "sidecar_invalid"
	// RejectKeyNotTrusted: the sidecar key id is in neither trust set.
	RejectKeyNotTrusted = "key_not_trusted"
	// RejectInvalidBundle: the bundle failed to parse, carries a malformed
	// manifest field (sequence grammar, trust_epoch), an unsupported version, a
	// pattern that does not compile, or no runnable rule.
	RejectInvalidBundle = "invalid_bundle"
	// RejectStateCommitFailed: the anti-downgrade state could not be durably
	// committed, so the candidate was NOT activated. Refusing to activate is the
	// safe outcome: activating without a durable watermark would reopen the
	// replay window FR-011a exists to close. Last-known-good keeps serving.
	RejectStateCommitFailed = "state_commit_failed"
)

// Degraded-coverage reasons (FR-007, FR-009a, FR-009b;
// contracts/bundle-status-surface.md §3). While any of these holds, scan-mode
// auto-approval is suspended and changes route to human review.
const (
	DegradedUnsignedExternalActive       = "unsigned_external_active"
	DegradedEmbeddedBelowWatermark       = "embedded_below_watermark"
	DegradedOperatorBundleMissing        = "operator_bundle_missing"
	DegradedOperatorAuthorityUnbaselined = "operator_authority_unbaselined"
)

// FR-011 load ceilings. A candidate AT a ceiling loads; one unit over is
// rejected whole. Design may tighten these, never loosen them.
const (
	// MaxBundleBytes is the bundle byte ceiling (8 MiB).
	MaxBundleBytes = 8 << 20
	// MaxBundleRules is the rules[] entry ceiling (all entries, runnable or not).
	MaxBundleRules = 2000
	// MaxSidecarBytes is the sidecar byte ceiling (4 KiB).
	MaxSidecarBytes = 4 << 10
)
