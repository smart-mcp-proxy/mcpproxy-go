// MCP-1112 (#598): official-registry server names contain '/'
// (e.g. "io.github.owner/repo"). The server-detail route is a single
// `:serverName` segment, so the name MUST be percent-encoded — otherwise the
// '/' splits the path into two segments and the request falls through to the
// catch-all 404. vue-router decodes the param back to the original name when it
// is read via `route.params.serverName` / the component prop, so callers read
// the plain name without any manual decode.
export function serverDetailPath(name: string, tab?: string): string {
  const base = `/servers/${encodeURIComponent(name)}`
  return tab ? `${base}?tab=${encodeURIComponent(tab)}` : base
}

// serverDisplayName prefers the registry-provided human-friendly `title` over
// the raw reverse-DNS `name` identifier (e.g. "io.github.owner/repo"). The
// `name` remains the stable key used for API calls and routing; only the
// rendered label changes.
export function serverDisplayName(server: { name: string; title?: string }): string {
  return server.title?.trim() || server.name
}
