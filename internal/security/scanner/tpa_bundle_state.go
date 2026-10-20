package scanner

import "time"

// Signing authorities (spec 101 FR-004/FR-004a). An authority is a NAMESPACE
// for anti-downgrade state, not a key: every publisher key — including keys
// being rotated in or out — belongs to the one publisher authority, so a
// rotated-in key inherits the existing watermark instead of starting fresh.
const (
	// AuthorityPublisher is the binary-embedded publisher signer set.
	AuthorityPublisher = "publisher"
	// AuthorityOperator is the config-resident operator signer set
	// (security.tpa_operator_signers).
	AuthorityOperator = "operator"
)

// TPABundleStateSchemaVersion versions the persisted TPABundleState record.
const TPABundleStateSchemaVersion = 1

// TPABundleState is the per-install anti-downgrade record (spec 101 FR-009,
// FR-010, FR-011a; data-model §4). It is persisted as ONE value and written in
// ONE storage transaction together with the activation-history row of the
// activation that produced it, so a crash can never pair a new active bundle
// with an old watermark, or a preserved rollback with a lost deny-list.
//
// Everything here is data-directory resident and therefore BEST-EFFORT against
// an attacker who can substitute config.db (spec threat-model boundary,
// SC-012): the guarantees that survive that attacker are the ones anchored
// outside the data dir — the publisher baseline re-seeded from the binary, the
// config-resident operator baseline, require_signed_bundle and the active-root
// declaration. Code MUST NOT present the ratchet, watermark, pin or deny-list
// as surviving state substitution.
type TPABundleState struct {
	SchemaVersion int `json:"schema_version"`

	// SignatureRatchet is set once ANY signature-verified external bundle has
	// activated on this install. From then on unsigned candidates are refused
	// regardless of require_signed_bundle (FR-009).
	SignatureRatchet bool `json:"signature_ratchet"`

	// Active is the external bundle currently serving, or nil when the
	// embedded default serves. Its digest names the content-addressed
	// last-known-good file tpa-lkg/<FullDigest>.json in the data directory.
	Active *TPABundleActiveRecord `json:"active,omitempty"`

	// Authorities holds per-authority scoped state keyed by AuthorityPublisher
	// / AuthorityOperator. A missing entry means "no state yet" and is seeded
	// from the authority's baseline (FR-009b) on first use.
	Authorities map[string]*TPAAuthorityState `json:"authorities,omitempty"`
}

// TPABundleActiveRecord describes the serving external bundle (FR-011a).
type TPABundleActiveRecord struct {
	// Authority is AuthorityPublisher / AuthorityOperator for a
	// signature-verified bundle, "" for an unsigned one.
	Authority string `json:"authority,omitempty"`
	// TrustEpoch is the manifest epoch (signed bundles only).
	TrustEpoch uint64 `json:"trust_epoch"`
	// Sequence is the manifest sequence (signed bundles only; 0 when unsigned).
	Sequence uint64 `json:"sequence"`
	// FullDigest is the lowercase-hex SHA-256 of the exact bundle bytes.
	FullDigest string `json:"full_digest"`
	// SidecarDigest is the lowercase-hex SHA-256 of the sidecar bytes ("" when
	// unsigned) — part of the idempotence key.
	SidecarDigest     string    `json:"sidecar_digest,omitempty"`
	SignatureVerified bool      `json:"signature_verified"`
	Source            string    `json:"source"`
	Path              string    `json:"path,omitempty"`
	GeneratedAt       string    `json:"generated_at,omitempty"`
	ActivatedAt       time.Time `json:"activated_at"`
	// Rollback is true when this bundle was activated by an explicit
	// operator rollback (FR-010) rather than normal watermark governance.
	Rollback bool `json:"rollback,omitempty"`
}

// TPAAuthorityState is one authority's anti-downgrade state, scoped to Epoch
// (FR-005a: every comparison is (authority, epoch, sequence), epoch first).
// When the current trusted epoch for the authority differs from Epoch the whole
// record is re-scoped: the watermark re-baselines from the authority's baseline
// and prior-epoch pins and deny entries are retired (loudly logged).
type TPAAuthorityState struct {
	Epoch uint64 `json:"epoch"`
	// Watermark is the highest sequence ever activated for (authority, Epoch),
	// never below the authority's baseline. Monotonic: rollback does not lower
	// it (FR-010).
	Watermark uint64 `json:"watermark"`
	// WatermarkSetBy is the full digest of the bundle that set Watermark ("" when
	// the watermark is only the seeded baseline).
	WatermarkSetBy string `json:"watermark_set_by,omitempty"`
	// ExternalEverActivated records that an external bundle of this authority
	// has ever activated on this install. It SURVIVES a trust-epoch re-scope:
	// the bump is what makes that corpus "no longer valid", so FR-009a(b)
	// (`operator_bundle_missing`) must still tell "operator bundle lost or
	// invalidated" from "operator root declared, nothing ever activated". Only
	// a newly activated bundle (which serves externally) makes it moot.
	ExternalEverActivated bool `json:"external_ever_activated,omitempty"`
	// Pin is the FR-010 rollback pin. It binds only while this authority is the
	// active root and Epoch equals the current trusted epoch; switching roots
	// suspends (never deletes) it.
	Pin *TPABundlePin `json:"pin,omitempty"`
	// DenyList holds the releases rolled away from (FR-010). Same scoping as Pin.
	DenyList []TPABundleDenyEntry `json:"deny_list,omitempty"`
}

// TPABundlePin is an operator-pinned rollback target, identified by full digest.
type TPABundlePin struct {
	Sequence   uint64 `json:"sequence"`
	FullDigest string `json:"full_digest"`
	// Embedded is true when the pin is the embedded default.
	Embedded bool      `json:"embedded,omitempty"`
	PinnedAt time.Time `json:"pinned_at"`
	// SidecarDigest, Source and Path identify the exact (bundle, sidecar) pair
	// kept in the last-known-good store for this target, so the pin can restore
	// it after the active root was switched away and back (FR-004b). Empty for
	// the embedded default and for an unsigned target.
	SidecarDigest string `json:"sidecar_digest,omitempty"`
	Source        string `json:"source,omitempty"`
	Path          string `json:"path,omitempty"`
}

// TPABundleDenyEntry is one deny-listed release, keyed (authority, epoch,
// sequence, digest) — the authority and epoch are the enclosing
// TPAAuthorityState's.
type TPABundleDenyEntry struct {
	Sequence   uint64    `json:"sequence"`
	FullDigest string    `json:"full_digest"`
	DeniedAt   time.Time `json:"denied_at"`
}

// TPABundleActivation is one append-only activation-history row (FR-010,
// SC-010; contracts/bundle-status-surface.md §5). The history makes a
// legitimate rollback POSSIBLE — it is how an operator learns the full digest
// of a known-good artifact — it never AUTHORIZES one.
type TPABundleActivation struct {
	Authority  string `json:"authority,omitempty"`
	TrustEpoch uint64 `json:"trust_epoch"`
	// Sequence is the decimal-string sequence as published ("" when unsigned or
	// embedded without one).
	Sequence          string    `json:"sequence,omitempty"`
	FullDigest        string    `json:"full_digest"`
	GeneratedAt       string    `json:"generated_at,omitempty"`
	ActivatedAt       time.Time `json:"activated_at"`
	Source            string    `json:"source"`
	SignatureVerified bool      `json:"signature_verified"`
	Rollback          bool      `json:"rollback,omitempty"`
}

// TPABundleStateStore persists TPABundleState and its activation history. It is
// implemented by *storage.Manager (storage imports this package, so the
// interface lives here — the same pattern as IncarnationStorage).
type TPABundleStateStore interface {
	// LoadTPABundleState returns the persisted state, or (nil, nil) when none
	// has ever been written. A read MUST NOT create the bucket.
	LoadTPABundleState() (*TPABundleState, error)
	// CommitTPABundleState writes state and, when entry is non-nil, appends it
	// to the activation history — both in ONE storage transaction (FR-011a).
	CommitTPABundleState(state *TPABundleState, entry *TPABundleActivation) error
	// ListTPABundleHistory returns every history row, oldest first.
	ListTPABundleHistory() ([]TPABundleActivation, error)
}
