// A listener address is the server-side bind target. Browser clients must use
// the public origin that served the UI, including its scheme, host, and port.
export function browserMcpEndpoint(path = '/mcp', origin = window.location.origin): string {
  const publicOrigin = new URL(origin).origin
  const endpoint = new URL(path, publicOrigin)
  return new URL(`${endpoint.pathname}${endpoint.search}${endpoint.hash}`, publicOrigin).href
}

// One-time client snippets may contain profile-specific paths, query values,
// headers, or credentials. Rewrite only the URL origin and leave the rest of
// the configuration intact. Invalid snippets remain available unchanged.
export function browserMcpSnippet(snippet: string, origin = window.location.origin): string {
  try {
    const config = JSON.parse(snippet)
    const proxy = config.mcpServers?.mcpproxy as { url?: string } | undefined
    if (typeof proxy?.url === 'string') proxy.url = browserMcpEndpoint(proxy.url, origin)
    return JSON.stringify(config, null, 2)
  } catch {
    return snippet
  }
}
