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
  if (runnable === 0) {
    // Zero runnable rules means offline TPA coverage is OFF: nothing is being
    // matched, so a scan verdict carries no signal at all. This outranks the
    // load-error warning — a plain "0" in the default tone read as healthy.
    tone = 'text-error'
    detail = `${detail} — no signatures running`
  }

  return {
    title: 'Signatures (runnable)',
    value: String(runnable),
    detail,
    tooltip: tooltipParts.join(' · '),
    tone,
  }
}
