import type { Server } from '@/types'
import { healthStatusLabel } from '@/utils/health'
import { effectiveStatus } from '@/utils/serversList'

// Spec 134 FR-022: the one-line reason of a Servers row. Pure; never invents
// text: every word comes from a known pattern table, the structured diagnostic,
// the last error, `health.summary` or the status label.

const REAUTH_CODES = ['MCPX_OAUTH_REAUTH_REQUIRED', 'MCPX_OAUTH_REFRESH_EXPIRED', 'MCPX_OAUTH_REFRESH_403']

interface Pattern {
  codes?: string[]
  text?: RegExp
  sentence: (server: Server) => string
}

// Order matters: the first pattern that matches wins.
const PATTERNS: Pattern[] = [
  {
    codes: ['MCPX_STDIO_SPAWN_ENOENT', 'MCPX_DOCKER_EXEC_NOT_FOUND', 'MCPX_CONFIG_INVALID_COMMAND'],
    text: /executable file not found|command not found|not found in \$?path|\benoent\b|no such file or directory/i,
    sentence: s => (s.command ? `Command not found: ${s.command}` : 'Command not found'),
  },
  {
    codes: ['MCPX_HTTP_CONN_REFUSED'],
    text: /connection refused|econnrefused/i,
    sentence: () => 'Connection refused',
  },
  {
    codes: REAUTH_CODES,
    text: /refresh token.*(expired|revoked)|token.*expired|invalid_grant|re-?auth/i,
    sentence: () => 'Sign-in expired',
  },
  {
    codes: ['MCPX_CONFIG_MISSING_SECRET'],
    text: /(secret|keyring|environment variable).{0,40}(not found|missing|unresolved|not set|could not)/i,
    sentence: () => 'Missing secret',
  },
  {
    codes: ['MCPX_HTTP_TIMEOUT', 'MCPX_STDIO_HANDSHAKE_TIMEOUT'],
    text: /timed? ?out|deadline exceeded/i,
    sentence: () => 'Timed out',
  },
  {
    codes: ['MCPX_CONFIG_PARSE_ERROR'],
    text: /invalid (config|configuration)|config(uration)? (is )?invalid/i,
    sentence: () => 'Invalid configuration',
  },
]

function oneLine(text: string): string {
  return text.replace(/\s+/g, ' ').trim()
}

function lastSegment(raw: string): string {
  const parts = raw.split(': ')
  return parts[parts.length - 1] || raw
}

/** Known failure pattern in the diagnostic, the last error or the health detail. */
export function failurePattern(server: Server): string {
  const code = (server.diagnostic?.code ?? server.error_code ?? '').toUpperCase()
  const haystack = [server.diagnostic?.user_message, server.diagnostic?.cause, server.last_error, server.health?.detail]
    .filter((v): v is string => !!v)
    .join('\n')
  for (const p of PATTERNS) {
    if (code && p.codes?.includes(code)) return p.sentence(server)
    if (haystack && p.text?.test(haystack)) return p.sentence(server)
  }
  return ''
}

export function serverReason(server: Server): string {
  const status = effectiveStatus(server)
  const health = server.health
  // A usable server's old error is not its reason: the summary says what it is.
  if (status !== 'ready') {
    const known = failurePattern(server)
    if (known) return known
  }
  // The card's detail, unchanged (ServerCard.statusDetail): while administratively
  // enabled the core's plain-language summary ("Host not found") leads; for a
  // disabled or quarantined server that summary describes the admin state, so
  // the structured diagnostic, then the last error's root cause, come first.
  const label = health?.status ? healthStatusLabel(health.status) : ''
  if (health?.summary && health.admin_state === 'enabled' && health.summary !== label) return oneLine(health.summary)
  const message = server.diagnostic?.user_message
  if (message) return oneLine(message)
  if (server.last_error) return oneLine(lastSegment(server.last_error))
  if (health?.summary) return oneLine(health.summary)
  if (health?.status) return label
  // Old core with no health object: the present legacy words.
  if (server.connected) return 'Connected'
  if (server.connecting) return 'Connecting'
  return 'Disconnected'
}
