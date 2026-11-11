import type { Server } from '@/types'

// Spec 123: the stored OAuth token cannot be decrypted with the configured
// key. A sign-in could not overwrite that row, so the server needs the
// configure guidance (restore the key, or `mcpproxy auth logout`), not a login.
export const OAUTH_TOKEN_UNDECRYPTABLE_CODE = 'MCPX_OAUTH_TOKEN_UNDECRYPTABLE'

export function isOAuthTokenUndecryptable(server: Server): boolean {
  return server.diagnostic?.code === OAUTH_TOKEN_UNDECRYPTABLE_CODE ||
    server.error_code === OAUTH_TOKEN_UNDECRYPTABLE_CODE
}

// Account switching and recovery must remain available even when a timeout
// is classified as a reconnect error, or a background connection is running.
export function canStartOAuthLogin(server: Server): boolean {
  if (isOAuthTokenUndecryptable(server)) return false
  const protocol = String(server.protocol)
  const remote = ['http', 'sse', 'streamable-http'].includes(protocol) ||
    (protocol === 'auto' && !!server.url && !server.command)
  return !!(server.enabled && remote && (
    server.oauth || server.authenticated || server.health?.action === 'login' ||
    server.health?.actions?.includes('login') || server.diagnostic?.code?.startsWith('MCPX_OAUTH_') ||
    /oauth/i.test(server.last_error || '')
  ))
}
