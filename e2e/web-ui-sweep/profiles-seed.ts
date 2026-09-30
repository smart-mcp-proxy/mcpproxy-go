// Shared REST seeding for the Profiles v3 sweep specs (Spec 108-i I25).
//
// Everything goes through the instance's REST API with MCPPROXY_API_KEY, and
// nothing writes a supported client's config file (the smoke instance's HOME is
// shared with the developer): the client is a CUSTOM client, which is a
// credential only. `anonymous_profile` (not require_mcp_auth) satisfies the
// FR-008a binding guard, so the other sweep specs still see an open instance.
const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

export const SERVER = process.env.SWEEP_SERVER_NAME || ''
export const RO_PROFILE = 'e2e-ro'
export const CLIENT_ID = 'e2e-laptop'
export const ORPHAN_ID = 'e2e-orphan'

export async function api(method: string, path: string, body?: unknown): Promise<{ status: number; data: any }> {
  const response = await fetch(`${BASE}/api/v1${path}`, {
    method,
    headers: { 'X-API-Key': KEY, 'Content-Type': 'application/json', 'X-MCPProxy-Client': 'webui/e2e' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let parsed: any = null
  try { parsed = text ? JSON.parse(text) : null } catch { parsed = text }
  return { status: response.status, data: parsed?.data ?? parsed }
}

/** Creates e2e-ro, points anonymous callers at it and adds the locked custom client. Idempotent. */
export async function seedProfiles(): Promise<void> {
  await api('POST', '/profiles', { name: RO_PROFILE, title: 'E2E Read-only', servers: [SERVER], max_tier: 'read' })
  await api('PATCH', '/config', { anonymous_profile: RO_PROFILE })
  await api('POST', '/clients', { id: CLIENT_ID, display_name: 'E2E laptop', profile: RO_PROFILE, mode: 'locked' })
}

/**
 * A client bound to a profile that is then deleted with force: its pin dangles, so the
 * row shows `profile_missing` (deny-all, never wider) and the Clients page a warning.
 * Needs seedProfiles() first (the binding guard compares against anonymous_profile).
 */
export async function seedMissingProfile(): Promise<void> {
  await api('POST', '/profiles', { name: 'e2e-gone', title: 'E2E gone', servers: [SERVER], max_tier: 'read' })
  await api('POST', '/clients', { id: ORPHAN_ID, profile: 'e2e-gone', mode: 'locked' })
  await api('DELETE', '/profiles/e2e-gone?force=true')
}

/** Removes everything a sweep spec created, in dependency order. Never throws. */
export async function cleanupProfiles(extraProfiles: string[] = [], tokens: string[] = []): Promise<void> {
  const quiet = async (run: () => Promise<unknown>) => { try { await run() } catch { /* best effort */ } }
  for (const token of tokens) {
    await quiet(() => api('DELETE', `/tokens/${encodeURIComponent(token)}`))
    await quiet(() => api('DELETE', `/tokens/${encodeURIComponent(token)}/permanent`))
  }
  await quiet(() => api('DELETE', `/clients/${CLIENT_ID}`))
  await quiet(() => api('DELETE', `/clients/${ORPHAN_ID}`))
  await quiet(() => api('PATCH', '/config', { anonymous_profile: '' }))
  for (const name of [RO_PROFILE, 'e2e-gone', ...extraProfiles]) {
    await quiet(() => api('DELETE', `/profiles/${encodeURIComponent(name)}?force=true`))
  }
}
