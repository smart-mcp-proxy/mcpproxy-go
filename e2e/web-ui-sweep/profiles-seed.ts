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

// ---- Spec 108-j (profiles-scope.spec.ts): attributed activity -----------------

export const SCOPE_PROFILE = 'e2e-scope-ro'
export const SCOPE_CLIENT = 'e2e-scope'

/** One MCP session with a client credential: initialize, then each call_tool_read in order. */
async function mcpSession(credential: string, calls: Array<{ name: string }>): Promise<void> {
  const endpoint = `${BASE}/mcp`
  const rpc = async (body: unknown, sessionId?: string) => {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      Accept: 'application/json, text/event-stream',
      'X-API-Key': credential,
    }
    if (sessionId) headers['Mcp-Session-Id'] = sessionId
    const response = await fetch(endpoint, { method: 'POST', headers, body: JSON.stringify(body) })
    await response.text()
    // A refused or failed MCP request would leave the sweep with no attributed
    // activity and every later check failing with a misleading fixture error.
    if (!response.ok) throw new Error(`MCP request failed with HTTP ${response.status}`)
    return response.headers.get('mcp-session-id') || sessionId || ''
  }
  const sessionId = await rpc({
    jsonrpc: '2.0', id: 1, method: 'initialize',
    params: { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'e2e-scope-client', version: '1.0' } },
  })
  await rpc({ jsonrpc: '2.0', method: 'notifications/initialized' }, sessionId)
  let id = 2
  for (const call of calls) {
    await rpc({ jsonrpc: '2.0', id: id++, method: 'tools/call', params: { name: 'call_tool_read', arguments: { name: call.name, args_json: '{}' } } }, sessionId)
  }
}

/**
 * Seeds one locked client on a profile that denies `<SERVER>:echo`, then makes two
 * calls with its credential: `<SERVER>:ping` (allowed) and `<SERVER>:echo` (refused,
 * a `policy_decision` record with block_reason profile_rule). The fixture tools are
 * all read-only, so a deny rule is what makes the blocked row. Idempotent.
 */
export async function seedScopeActivity(): Promise<void> {
  // Idempotent: a leftover client or profile from an interrupted run would make
  // the POSTs below fail, so start from nothing; a failed seed removes what it made.
  await cleanupScopeActivity()
  try {
    await seedScopeActivityOnce()
  } catch (error) {
    await cleanupScopeActivity()
    throw error
  }
}

async function seedScopeActivityOnce(): Promise<void> {
  const profile = await api('POST', '/profiles', { name: SCOPE_PROFILE, title: 'E2E Scope RO', servers: [SERVER], tools: { deny: [`${SERVER}:echo`] }, max_tier: 'read' })
  if (profile.status >= 300) throw new Error(`seeding ${SCOPE_PROFILE} failed (status ${profile.status})`)
  const config = await api('PATCH', '/config', { anonymous_profile: SCOPE_PROFILE })
  if (config.status >= 300) throw new Error(`setting anonymous_profile failed (status ${config.status})`)
  const created = await api('POST', '/clients', { id: SCOPE_CLIENT, display_name: 'E2E Scope', profile: SCOPE_PROFILE, mode: 'locked' })
  const credential: string = created.data?.credential
  if (!credential) throw new Error(`seeding ${SCOPE_CLIENT} returned no credential (status ${created.status})`)
  await mcpSession(credential, [{ name: `${SERVER}:ping` }, { name: `${SERVER}:echo` }])
}

/** Removes what seedScopeActivity created. Never throws. */
export async function cleanupScopeActivity(): Promise<void> {
  const quiet = async (run: () => Promise<unknown>) => { try { await run() } catch { /* best effort */ } }
  await quiet(() => api('DELETE', `/clients/${SCOPE_CLIENT}`))
  await quiet(() => api('PATCH', '/config', { anonymous_profile: '' }))
  await quiet(() => api('DELETE', `/profiles/${SCOPE_PROFILE}?force=true`))
}
