// Spec 108-j J2 (FR-031, FR-045). The three REST filter names a scope-aware
// page may send, taken from the one source of truth: useScopeQuery's toRest(),
// which already hides a parameter the build does not advertise (rule 7) and
// lists per page which endpoints honour it (restPages). A page spreads the
// result into its own request builder, so no page can send a scope parameter
// its backend would refuse, and none builds the names by hand.

export interface ScopeRestParams {
  profile?: string
  client?: string
  token?: string
}

export function pickScopeParams(rest: Record<string, string> | null | undefined): ScopeRestParams {
  const out: ScopeRestParams = {}
  if (!rest) return out
  if (rest.profile) out.profile = rest.profile
  if (rest.client) out.client = rest.client
  if (rest.token) out.token = rest.token
  return out
}

/** A stable string of the picked params, for watchers that refetch when the
 * applied scope changes (a chip removed, a header chip set, the feature list
 * arriving after mount). */
export function scopeParamsKey(params: ScopeRestParams): string {
  return JSON.stringify([params.profile ?? '', params.client ?? '', params.token ?? ''])
}
