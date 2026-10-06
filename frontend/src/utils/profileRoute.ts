import type { RouteLocationRaw } from 'vue-router'

// Spec 108-j J9 (FR-045 named-route exception). The profile editor is
// /profiles/:name, a path parameter that useScopeQuery.linkTo cannot carry
// (it maps a page to a route name plus a query only). This is the one helper
// that builds that link, mirroring utils/serverRoute.ts, so no view assembles
// the route object by hand. `focus` is "server:tool" and lands the editor on
// that row.
export function profileEditorLink(name: string, focus?: string): RouteLocationRaw {
  return { name: 'profile-editor', params: { name }, query: focus ? { focus } : {} }
}
