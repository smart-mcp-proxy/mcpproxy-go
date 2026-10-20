import type { Server } from '@/types'

// Account switching and recovery must remain available even when a timeout
// is classified as a reconnect error, or a background connection is running.
export function canStartOAuthLogin(server: Server): boolean {
  const protocol = String(server.protocol)
  const remote = ['http', 'sse', 'streamable-http'].includes(protocol) ||
    (protocol === 'auto' && !!server.url && !server.command)
  return !!(server.enabled && remote && (
    server.oauth || server.authenticated || server.health?.action === 'login' ||
    server.health?.actions?.includes('login') || server.diagnostic?.code?.startsWith('MCPX_OAUTH_') ||
    /oauth/i.test(server.last_error || '')
  ))
}
