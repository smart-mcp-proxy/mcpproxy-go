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

// MCP-2125 (#643 Defect B): scan ids embed the raw upstream server name, so an
// official-registry server whose name contains '/' (e.g.
// "com.pulsemcp/google-flights") yields a scan id like
// "scan-com.pulsemcp/google-flights-1781284446323229000". The scan-report route
// is a single `:jobId` segment, so the id MUST be percent-encoded — otherwise
// the '/' splits the path and the link falls through to the catch-all 404.
// vue-router decodes the param back to the original id on read (same class as
// serverDetailPath above / MCP-1112).
export function scanReportPath(jobId: string): string {
  return `/security/scans/${encodeURIComponent(jobId)}`
}

// serverDisplayName prefers the registry-provided human-friendly `title` over
// the raw reverse-DNS `name` identifier (e.g. "io.github.owner/repo"). The
// `name` remains the stable key used for API calls and routing; only the
// rendered label changes.
export function serverDisplayName(server: { name: string; title?: string }): string {
  return server.title?.trim() || server.name
}
