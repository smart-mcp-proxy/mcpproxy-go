import { describe, it, expect, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

// Spec 109-l P8 / FR-005 ("never a dead link"): every fix.target the core emits
// for a Spec 108 attention kind resolves, in the real app router, to a
// registered route (not the catch-all) and carries the query the destination
// screen reads (focus, tab, token). The targets are the exact strings of the
// P1 table in internal/runtime/attention_108.go.
const TARGETS: Array<{ kind: string; target: string; route: string; query: Record<string, string> }> = [
  {
    kind: 'anonymous_denied_by_binding_guard',
    target: '/settings?tab=security&focus=require_mcp_auth',
    route: 'settings',
    query: { tab: 'security', focus: 'require_mcp_auth' },
  },
  { kind: 'client_holds_admin_key', target: '/clients?focus=cursor', route: 'clients', query: { focus: 'cursor' } },
  {
    kind: 'client_token_name_conflict',
    target: '/clients?tab=tokens&token=client-codex',
    route: 'clients',
    query: { tab: 'tokens', token: 'client-codex' },
  },
  { kind: 'profile_missing', target: '/clients?focus=codex', route: 'clients', query: { focus: 'codex' } },
  { kind: 'client_rotation_pending', target: '/clients?focus=codex', route: 'clients', query: { focus: 'codex' } },
  { kind: 'client_credential_expiring', target: '/clients?focus=codex', route: 'clients', query: { focus: 'codex' } },
  // A custom client id is encoded by the core (url.QueryEscape) and decodes back.
  { kind: 'client_holds_admin_key', target: '/clients?focus=my+client', route: 'clients', query: { focus: 'my client' } },
]

describe('Spec 108 attention fix targets resolve to real routes (Spec 109-l P8)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.head.innerHTML = '<meta name="mcpproxy-server-edition" content="false">'
  })

  it.each(TARGETS)('$kind -> $target', ({ target, route, query }) => {
    const resolved = appRouter.resolve(target)
    expect(resolved.name).not.toBe('not-found')
    expect(resolved.matched.length).toBeGreaterThan(0)
    expect(String(resolved.name)).toBe(route)
    expect(resolved.query).toMatchObject(query)
  })
})
