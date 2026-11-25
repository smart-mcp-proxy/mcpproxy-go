import type { Server } from '@/types'
import { healthStatusLabel, healthStatusText } from '@/utils/health'
import { effectiveStatus } from '@/utils/serversList'

// Spec 134 FR-022: the one-line reason of a Servers row. Pure; never invents
// text: every word comes from a known pattern table, the structured diagnostic,
// the last error, `health.summary` or the status label.

const REAUTH_CODES = ['MCPX_OAUTH_REAUTH_REQUIRED', 'MCPX_OAUTH_REFRESH_EXPIRED', 'MCPX_OAUTH_REFRESH_403']

interface Pattern {
  codes?: string[]
  /** Applied only when the core attached no classified diagnostic code (a code wins over free text). */
  text?: RegExp
  sentence: (server: Server) => string
}

// Order matters: the first pattern that matches wins.
const PATTERNS: Pattern[] = [
  {
    codes: ['MCPX_STDIO_SPAWN_ENOENT', 'MCPX_DOCKER_EXEC_NOT_FOUND'],
    text: /executable file not found|command not found|not found in \$?path/i,
    sentence: s => (s.command ? `Command not found: ${s.command}` : 'Command not found'),
  },
  {
    codes: ['MCPX_HTTP_CONN_REFUSED'],
    text: /connection refused|econnrefused/i,
    sentence: () => 'Connection refused',
  },
  {
    codes: REAUTH_CODES,
    text: /refresh token.*(expired|revoked)|invalid_grant/i,
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
  // A classified code is authoritative: check every pattern's codes first, and
  // fall to free text only when the core attached no code (or an unclassified one).
  for (const p of PATTERNS) {
    if (code && p.codes?.includes(code)) return p.sentence(server)
  }
  if (code && code !== 'MCPX_UNKNOWN_UNCLASSIFIED') return ''
  for (const p of PATTERNS) {
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
  // The structured diagnostic's message, then, while administratively enabled,
  // the core's plain-language summary ("Host not found") over the raw last
  // error. For a disabled or quarantined server that summary describes the
  // admin state, so the last error's root cause comes first.
  const message = server.diagnostic?.user_message
  if (message) return oneLine(message)
  const label = health?.status ? healthStatusLabel(health.status) : ''
  if (health?.summary && health.admin_state === 'enabled' && server.enabled && health.summary !== label) return oneLine(health.summary)
  if (server.last_error) return oneLine(lastSegment(server.last_error))
  // Disable updates `enabled` before `health` is refreshed: never show the old summary of a server just switched off.
  if (!server.enabled && health?.status === 'disabled') return label
  if (health?.summary) return oneLine(health.summary)
  if (health?.status) return label
  // A payload with a health object but neither status nor summary, or an old
  // core with none: the present legacy words (never "Connected" for a server
  // the core reported unusable).
  if (health) return healthStatusText(health, server.connected)
  if (server.connected) return 'Connected'
  if (server.connecting) return 'Connecting'
  return 'Disconnected'
}
