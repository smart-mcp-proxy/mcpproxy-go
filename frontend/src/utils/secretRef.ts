// TS port of internal/secret/refname.go RefName (Spec 109 FR-065). Replaces
// ServerDetail.vue's old `suggestSecretName`, which dropped the field KIND
// (env vs header) and so let an env var and a header of the same name
// collide on one keyring entry. Pinned against the shared fixture
// (frontend/tests/unit/fixtures/ref_names.json, copied from
// internal/secret/testdata/ref_names.json) so Go, this file and the Swift
// port agree on every case.

const REF_NAME_MAX_LEN = 64

function normalizeRefComponent(s: string): string {
  const lower = s.toLowerCase()
  const collapsed = lower.replace(/[^a-z0-9-]+/g, '-')
  const dedupedHyphens = collapsed.replace(/-{2,}/g, '-')
  let trimmed = dedupedHyphens.replace(/^-+|-+$/g, '')
  if (trimmed.length > REF_NAME_MAX_LEN) {
    trimmed = trimmed.slice(0, REF_NAME_MAX_LEN).replace(/-+$/g, '')
  }
  return trimmed
}

/**
 * refName computes the OS-keyring entry name for one field on one server
 * (FR-065): "<server>-<kind>-<key>", lower-cased, with every run of
 * characters outside [a-z0-9-] collapsed to a single '-', trimmed, and
 * capped at 64 characters. kind is normally "env" or "header" — keeping it a
 * distinct path component (rather than folding it into key) is what keeps an
 * env var and a header of the same name from colliding on one entry.
 *
 * `taken` reports whether a candidate name is already present in the
 * keyring (GET /secrets/refs); on a collision refName appends -2, -3, …
 * until it finds a free one, so an add never silently overwrites an
 * existing secret.
 */
export function refName(
  server: string,
  kind: 'env' | 'header',
  key: string,
  taken?: (name: string) => boolean
): string {
  const base = normalizeRefComponent(`${server}-${kind}-${key}`)
  if (!taken) return base

  let candidate = base
  let n = 2
  while (taken(candidate)) {
    const suffix = `-${n}`
    const maxBase = REF_NAME_MAX_LEN - suffix.length
    const trimmedBase = base.length > maxBase ? base.slice(0, maxBase) : base
    candidate = trimmedBase + suffix
    n++
  }
  return candidate
}
