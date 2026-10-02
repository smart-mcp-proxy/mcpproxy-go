import type { RouteLocationRaw } from 'vue-router'
import { isScopeParamAvailable } from '@/composables/useScopeQuery'

// Spec 109 D35 (T163, FR-054). The palette jumps to a client or an agent token
// on the Clients page. These are fresh navigations, so they carry NO sticky
// params: a sticky `profile` filter from the page the user was on could hide the
// very row they asked for. Without the scope filters (an older core) the same
// destinations degrade to the Clients page itself, never to a dead parameter.

/** The Clients page focused on one client: `/clients?client=<id>`. */
export function clientLink(id: string): RouteLocationRaw {
  // `focus` is the attention-fix landing of Spec 109-l: it highlights the row.
  return isScopeParamAvailable('client')
    ? { path: '/clients', query: { client: id } }
    : { path: '/clients', query: { focus: id } }
}

/** The Clients page, Agent Tokens tab, filtered to one token. */
export function tokenLink(name: string): RouteLocationRaw {
  return isScopeParamAvailable('token')
    ? { path: '/clients', query: { tab: 'tokens', token: name } }
    : { path: '/clients', query: { tab: 'tokens' } }
}
