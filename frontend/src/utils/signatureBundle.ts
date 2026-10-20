/**
 * Presentation helper for the security overview's `signature_bundle`
 * descriptor (spec 086 FR-019, GH #938 finding 2).
 *
 * Before this existed, no supported surface could answer "which signatures is
 * my proxy actually running, and how old are they?" — the offline TPA corpus
 * was embed-only, its single load report went to an unconfigured logger, and
 * neither the REST API nor the UI carried a version, count, or freshness
 * signal. A years-stale corpus was indistinguishable from a fresh export.
 */

export interface SignatureBundle {
  source?: string
  path?: string
  bundle_version?: string
  schema_version?: string
  signature_count?: number
  runnable_rules?: number
  skipped_rules?: number
  declared_skipped?: number
  generated_at?: string
  fingerprint?: string
  loaded_at?: string
  load_error?: string
  // --- Spec 101 FR-020 (contracts/bundle-status-surface.md) ---
  full_digest?: string
  sequence?: string
  authority?: string
  trust_epoch?: number
  signature_verified?: boolean
  staleness_days?: number
  coverage_degraded?: boolean
  degraded_reason?: string
  pinned?: boolean
  rollback?: boolean
  last_rejection?: BundleRejection | null
}

export interface BundleRejection {
  reason: string
  detail?: string
  at?: string
}

export interface IdentityRow {
  label: string
  value: string
}

export interface DegradedCoverage {
  reason: string
  /** Plain-language why, ending with what happens to scan-mode auto-approval. */
  explanation: string
}

export interface LastRejectionSummary {
  reason: string
  label: string
  detail: string
  at: string
}

export interface SignatureBundleSummary {
  /** Stat title. */
  title: string
  /** Big number: how many rules are LIVE in the offline tier. */
  value: string
  /** One-line provenance under the number. */
  detail: string
  /** Hover text carrying freshness + identity + any load failure. */
  tooltip: string
  /** Tailwind/DaisyUI tone class; empty means default. */
  tone: string
  /** 12-hex human label ('' when unknown). */
  shortFingerprint: string
  /** Complete SHA-256 ('' when the daemon does not report it) — shown on demand. */
  fullDigest: string
  /** Sequence / authority / epoch / signature / age rows, only those reported. */
  identity: IdentityRow[]
  /** Non-null ONLY when coverage is degraded: the one state that needs action. */
  degraded: DegradedCoverage | null
  /** Most recent refused candidate; last-known-good is still serving. */
  lastRejection: LastRejectionSummary | null
  pinned: boolean
  rollback: boolean
}

const SUSPENDED =
  'Scan-mode auto-approval is suspended, so tool changes go to review until this clears.'

const DEGRADED_TEXT: Record<string, string> = {
  unsigned_external_active:
    'An unsigned signature bundle (dropped in or fetched) is active, and its origin cannot be verified.',
  embedded_below_watermark:
    'The built-in signature set is older than a signed bundle this install has already used, so it may be missing newer signatures.',
  operator_bundle_missing:
    'The operator-signed bundle that was previously active is missing or no longer valid, and a fallback set is serving.',
  operator_authority_unbaselined:
    'An operator-signed bundle is active, but the operator authority has no baseline configured to check it against.',
}

/** Plain-language explanation for a degraded_reason; unknown codes stay visible. */
export function describeDegradedReason(reason: string | undefined): string {
  const why = reason && DEGRADED_TEXT[reason]
    ? DEGRADED_TEXT[reason]
    : reason
      ? `Signature coverage is degraded (${reason}).`
      : 'Signature coverage is degraded.'
  return `${why} ${SUSPENDED}`
}

const REJECTION_LABELS: Record<string, string> = {
  tamper: 'Tamper: signature did not verify',
  downgrade: 'Downgrade refused',
  stale_epoch_replay: 'Stale epoch replay refused',
  epoch_ahead: 'Bundle epoch is newer than this install trusts (publisher bundle: update mcpproxy; operator bundle: check tpa_operator_epoch)',
  unsigned_refused_by_ratchet: 'Unsigned bundle refused after a signed one was used',
  signature_required: 'Signature required but missing',
  authority_not_active: 'Signed by the non-active authority',
  rollback_target_mismatch: 'Rollback target digest mismatch',
  deny_listed: 'Rolled-away release refused',
  gate_failure: 'Activation self-check failed',
  ceiling_exceeded: 'Size or rule-count ceiling exceeded',
  not_regular_file: 'Not a regular file',
  read_timeout: 'Read timed out',
  read_error: 'Could not read the bundle',
  sidecar_invalid: 'Signature sidecar invalid',
  key_not_trusted: 'Signing key not trusted',
  invalid_bundle: 'Bundle invalid',
  state_commit_failed: 'Could not record anti-downgrade state',
}

function rejectionLabel(reason: string): string {
  return REJECTION_LABELS[reason] ?? reason.replace(/_/g, ' ')
}

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? '' : 's'}`
}

/**
 * Formats a bundle descriptor for the Security tab. Returns null when the
 * daemon reports nothing (an older backend), so the UI renders no empty
 * chrome rather than a misleading zero.
 */
export function formatSignatureBundle(
  bundle: SignatureBundle | null | undefined
): SignatureBundleSummary | null {
  if (!bundle || Object.keys(bundle).length === 0) return null

  const runnable = bundle.runnable_rules ?? 0
  const source = bundle.source || 'unknown'
  const version = bundle.bundle_version ? ` v${bundle.bundle_version}` : ''

  let detail = source === 'file' && bundle.path
    ? `${bundle.path}${version}`
    : `${source}${version}`

  const tooltipParts: string[] = []
  if (bundle.generated_at) tooltipParts.push(`Generated ${bundle.generated_at}`)
  if (bundle.fingerprint) tooltipParts.push(`Fingerprint ${bundle.fingerprint}`)
  if (typeof bundle.skipped_rules === 'number' || typeof bundle.declared_skipped === 'number') {
    tooltipParts.push(
      `${bundle.skipped_rules ?? 0} not runnable offline, ${bundle.declared_skipped ?? 0} declared-skipped`
    )
  }

  let tone = ''
  if (bundle.load_error) {
    // The configured bundle could not be loaded; the previously active corpus
    // is still live. Say so — the counts alone would imply all is well.
    tone = 'text-warning'
    detail = `${detail} — configured bundle load failed`
    tooltipParts.push(bundle.load_error)
  }
  // Degraded coverage needs action, so it gets the warning tone (the banner
  // carries the why). Zero runnable rules below still outranks it.
  const isDegraded = bundle.coverage_degraded === true
  if (isDegraded && tone === '') tone = 'text-warning'
  if (runnable === 0) {
    // Zero runnable rules means offline TPA coverage is OFF: nothing is being
    // matched, so a scan verdict carries no signal at all. This outranks the
    // load-error warning — a plain "0" in the default tone read as healthy.
    tone = 'text-error'
    detail = `${detail} — no signatures running`
  }

  const fullDigest = bundle.full_digest || ''
  const shortFingerprint = bundle.fingerprint || fullDigest.slice(0, 12)

  const identity: IdentityRow[] = []
  if (bundle.sequence) identity.push({ label: 'Sequence', value: bundle.sequence })
  if (bundle.authority) identity.push({ label: 'Authority', value: bundle.authority })
  if (typeof bundle.trust_epoch === 'number') {
    identity.push({ label: 'Trust epoch', value: String(bundle.trust_epoch) })
  }
  identity.push({
    label: 'Signature',
    value: bundle.signature_verified ? 'verified' : 'not verified',
  })
  if (typeof bundle.staleness_days === 'number') {
    identity.push({ label: 'Age', value: plural(bundle.staleness_days, 'day') })
  }

  const rej = bundle.last_rejection
  const lastRejection: LastRejectionSummary | null = rej && rej.reason
    ? { reason: rej.reason, label: rejectionLabel(rej.reason), detail: rej.detail ?? '', at: rej.at ?? '' }
    : null

  return {
    title: 'Signatures (runnable)',
    value: String(runnable),
    detail,
    tooltip: tooltipParts.join(' · '),
    tone,
    shortFingerprint,
    fullDigest,
    identity,
    degraded: isDegraded
      ? { reason: bundle.degraded_reason ?? '', explanation: describeDegradedReason(bundle.degraded_reason) }
      : null,
    lastRejection,
    pinned: bundle.pinned === true,
    rollback: bundle.rollback === true,
  }
}
