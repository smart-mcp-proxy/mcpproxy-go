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
  // --- Spec 087 / Spec 101 US3 freshness fields (tasks B15) ---
  last_checked_at?: string
  last_activated_at?: string
  stale?: boolean
  stale_after_days?: number
  refresh?: BundleRefresh | null
  reprotect?: BundleReprotect | null
}

/** Refresh-cycle status. Carries the fetch HOST only, never the URL. */
export interface BundleRefresh {
  fetch_enabled?: boolean
  fetch_host?: string
  interval_seconds?: number
  last_trigger?: string
  last_outcome?: string
  last_reason?: string
  /** The fetch leg's own last answer, apart from the merged cycle outcome. */
  fetch_state?: string
  fetch_reason?: string
  last_attempt_at?: string
  consecutive_failures?: number
  next_check_at?: string
}

/** Post-activation re-protection sweep status (Spec 101 FR-019). */
export interface BundleReprotect {
  state?: string
  reason?: string
  last_completed_at?: string
  servers_scanned?: number
  servers_with_hits?: number
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

/** B16-style conditions: states of the refresh itself that need attention. */
export type RefreshProblemKind = 'stale' | 'stuck' | 'fetch_inactive'

export interface RefreshProblem {
  kind: RefreshProblemKind
  /** Plain-language why, carrying no digest, path, URL or host. */
  explanation: string
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
  /**
   * The first B16-style refresh condition (stale corpus, stuck on
   * last-known-good, fetch configured but inactive), or null. Separate from
   * `degraded`, which stays coverage-only.
   */
  refreshProblem: RefreshProblem | null
  /** Every refresh condition that holds, in priority order. */
  refreshProblems: RefreshProblem[]
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
  tamper: 'Tamper: the signature or the signing-key identity check failed',
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

const OUTCOME_LABELS: Record<string, string> = {
  activated: 'Activated a new bundle',
  // Neutral on purpose: `unchanged` also covers a candidate the pipeline already
  // refused, and `not_modified` only says the remote resource did not change.
  // Neither establishes that the serving corpus is current.
  unchanged: 'No change',
  not_modified: 'Remote not modified',
  rejected: 'Candidate refused',
  fetch_unavailable: 'Fetch unavailable',
  channel_invalid: 'Channel invalid',
  superseded: 'Superseded by a settings change',
  disabled: 'Fetch disabled',
}

const REASON_LABELS: Record<string, string> = {
  network_error: 'network error',
  timeout: 'timed out',
  http_status: 'unexpected HTTP status',
  rate_limited: 'rate limited',
  fetch_off: 'fetch is off',
  no_url: 'no URL configured',
  invalid_url: 'URL is invalid',
  no_trusted_signer: 'no trusted signer configured',
  state_unavailable: 'anti-downgrade state unavailable',
}

const REPROTECT_REASON_LABELS: Record<string, string> = {
  auto_baseline_scan_off: 'the automatic baseline scan is off',
}

function words(code: string): string {
  return code.replace(/_/g, ' ')
}

/** `2026-10-21T06:00:00Z` -> `2026-10-21 06:00 UTC`; anything unparsable stays as sent. */
function formatWhen(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return iso
  return `${new Date(t).toISOString().slice(0, 16).replace('T', ' ')} UTC`
}

function refreshRowValue(r: BundleRefresh): string {
  const outcome = r.last_outcome
  let text = outcome ? (OUTCOME_LABELS[outcome] ?? words(outcome)) : 'No cycle has run yet'
  if (outcome && r.last_reason) {
    text += ` (${REASON_LABELS[r.last_reason] ?? words(r.last_reason)})`
  }
  if (r.next_check_at) text += `, next check ${formatWhen(r.next_check_at)}`
  return text
}

function reprotectRowValue(r: BundleReprotect): string {
  let text = r.state ?? ''
  if (r.reason) text += ` (${REPROTECT_REASON_LABELS[r.reason] ?? words(r.reason)})`
  if (typeof r.servers_scanned === 'number' && r.servers_scanned > 0) {
    text += `, ${r.servers_with_hits ?? 0} of ${r.servers_scanned} ${r.servers_scanned === 1 ? 'server' : 'servers'} matched`
  }
  if (r.last_completed_at) text += `, last completed ${formatWhen(r.last_completed_at)}`
  return text
}

/** Reasons that mean "the pipeline correctly refused a replay"; never a problem. */
const QUIET_REJECTIONS = new Set(['downgrade', 'deny_listed'])
const FETCH_CONFIG_REASONS = new Set(['no_url', 'invalid_url', 'no_trusted_signer'])
const FAILURE_WARN_AT = 3

/**
 * The three B16 conditions, mirroring scanner.BundleDoctorWarnings so the Web UI
 * and `mcpproxy doctor` never disagree: a stale corpus on an install that can
 * refresh, a refresh stuck on last-known-good, and fetch configured but inactive.
 */
function refreshProblemsOf(b: SignatureBundle): RefreshProblem[] {
  const out: RefreshProblem[] = []
  const refresh = b.refresh ?? null
  const fetchOn = refresh?.fetch_enabled === true

  if (b.stale === true && (b.source === 'file' || b.source === 'fetch' || fetchOn)) {
    const age = typeof b.staleness_days === 'number' ? plural(b.staleness_days, 'day') + ' old' : 'past its freshness threshold'
    const threshold = typeof b.stale_after_days === 'number' ? ` (the threshold is ${plural(b.stale_after_days, 'day')})` : ''
    out.push({
      kind: 'stale',
      explanation: `The signature corpus is ${age}${threshold}, so newer signatures may be missing. Run "mcpproxy security bundle refresh" or check the refresh source.`,
    })
  }

  const rej = b.last_rejection
  let stuck = ''
  if (rej && rej.reason && !QUIET_REJECTIONS.has(rej.reason)) {
    const since = Date.parse(b.last_activated_at ?? b.loaded_at ?? '')
    const at = Date.parse(rej.at ?? '')
    if (!Number.isNaN(at) && (Number.isNaN(since) || at > since)) {
      stuck = `A newer candidate was refused (${rejectionLabel(rej.reason)}); the previous bundle is still serving as last-known-good.`
    }
  }
  if (!stuck && (refresh?.consecutive_failures ?? 0) >= FAILURE_WARN_AT) {
    stuck = `The last ${refresh!.consecutive_failures} refresh attempts failed; the previous bundle is still serving as last-known-good.`
  }
  if (stuck) out.push({ kind: 'stuck', explanation: stuck })

  if (
    fetchOn &&
    refresh?.fetch_state === 'disabled' &&
    refresh.fetch_reason &&
    FETCH_CONFIG_REASONS.has(refresh.fetch_reason)
  ) {
    out.push({
      kind: 'fetch_inactive',
      explanation: `Fetching is enabled but not running: ${REASON_LABELS[refresh.fetch_reason]}. Check the tpa_fetch_url, tpa_active_root and tpa_operator_signers settings.`,
    })
  }
  return out
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
  // Spec 087 FR-017: when the corpus was last checked and activated, how the
  // last refresh cycle went, and the re-protection sweep. Each row appears only
  // when the daemon reports it, so an older backend renders exactly as before.
  if (bundle.last_checked_at) {
    identity.push({ label: 'Last checked', value: formatWhen(bundle.last_checked_at) })
  }
  if (bundle.last_activated_at) {
    identity.push({ label: 'Last activated', value: formatWhen(bundle.last_activated_at) })
  }
  const refresh = bundle.refresh ?? null
  if (refresh && (refresh.last_outcome || refresh.next_check_at)) {
    identity.push({ label: 'Refresh', value: refreshRowValue(refresh) })
  }
  if (refresh?.fetch_enabled && refresh.fetch_host) {
    identity.push({ label: 'Fetch host', value: refresh.fetch_host })
  }
  if (bundle.reprotect?.state) {
    identity.push({ label: 'Re-protection', value: reprotectRowValue(bundle.reprotect) })
  }

  const rej = bundle.last_rejection
  const lastRejection: LastRejectionSummary | null = rej && rej.reason
    ? { reason: rej.reason, label: rejectionLabel(rej.reason), detail: rej.detail ?? '', at: rej.at ?? '' }
    : null

  const refreshProblems = refreshProblemsOf(bundle)

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
    refreshProblem: refreshProblems[0] ?? null,
    refreshProblems,
    pinned: bundle.pinned === true,
    rollback: bundle.rollback === true,
  }
}
