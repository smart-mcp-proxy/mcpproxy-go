import type { ClientPresence, ProfileView } from '@/types/api'

// Builders for the Profiles v3 wire shapes (Spec 108-f) used by the 108-i specs.

export function makeProfile(name: string, extra: Partial<ProfileView> = {}): ProfileView {
  return {
    name,
    servers: ['github'],
    effective_servers: ['github'],
    effective_unannotated: 'deny',
    effective_code_execution: false,
    is_legacy: false,
    tool_counts: { read: 3, write: 0, destructive: 0, unannotated_hidden: 0 },
    tool_count: 3,
    calls_24h: 0,
    blocked_24h: 0,
    ...extra,
  }
}

export function makeClient(id: string, extra: Partial<ClientPresence> = {}): ClientPresence {
  return {
    id,
    display_name: id.charAt(0).toUpperCase() + id.slice(1),
    kind: 'supported',
    state: 'installed',
    installed: true,
    connected: true,
    last_seen: null,
    active_sessions: 0,
    calls_24h: 0,
    credential_state: 'client',
    blocked_24h: 0,
    ...extra,
  } as ClientPresence
}

export const WORK_RO = makeProfile('work-ro', { title: 'Work', max_tier: 'read', servers: ['github', 'notion'], effective_servers: ['github', 'notion'] })
export const WORK_FULL = makeProfile('work-full', { title: 'Work Full', max_tier: 'destructive', management_tools: true })

// An ApiError-shaped rejection for mocked api methods.
export function apiError(message: string, extra: Record<string, unknown> = {}): Error {
  return Object.assign(new Error(message), { name: 'ApiError', ...extra })
}

export const GUARD_REFUSAL = {
  error: 'this change would leave cursor reachable without authentication',
  code: 'binding_bypassable_without_auth',
  status: 409,
  bindings: [{ client_id: 'cursor', token_name: 'client-cursor', profile: 'work-ro', mode: 'switchable' }],
  fixes: [{ kind: 'require_mcp_auth' }, { kind: 'set_anonymous_profile', target: 'work-ro' }],
}
