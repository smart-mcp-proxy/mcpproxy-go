import { describe, it, expect } from 'vitest'
import { serverReason, failurePattern } from '@/utils/serverReason'
import type { Server } from '@/types'

// Spec 134 FR-022: pattern, then the diagnostic or short last error, then
// health.summary, then the status label. Never invented text.

function srv(over: Partial<Server> & { status?: string; summary?: string } = {}): Server {
  const { status, summary, ...rest } = over as any
  return {
    name: 'srv', protocol: 'stdio', enabled: true, quarantined: false, connected: false, connecting: false, tool_count: 0,
    health: status ? { level: 'unhealthy', admin_state: 'enabled', summary: summary ?? '', status, usable: status === 'ready', actions: [] } : undefined,
    ...rest,
  } as Server
}

describe('serverReason', () => {
  it.each([
    ['exec: "npx": executable file not found in $PATH', 'Command not found: uvx', 'uvx'],
    ['dial tcp 127.0.0.1:9: connect: connection refused', 'Connection refused', undefined],
    ['context deadline exceeded', 'Timed out', undefined],
    ['secret "API_KEY" not found in keyring', 'Missing secret', undefined],
    ['invalid configuration: url missing', 'Invalid configuration', undefined],
    ['oauth: refresh token expired', 'Sign-in expired', undefined],
  ])('known pattern %s', (error, expected, command) => {
    const s = srv({ status: 'error', last_error: `failed to connect: ${error}`, command })
    expect(serverReason(s)).toBe(command ? expected : expected)
    expect(failurePattern(s)).not.toBe('')
  })

  it('a classified code beats free text: a timeout mentioning a missing path is a timeout, npx with nothing to run is not "not found"', () => {
    const timeout = srv({ status: 'error', last_error: 'x /etc/foo: no such file or directory executable file not found', diagnostic: { code: 'MCPX_STDIO_HANDSHAKE_TIMEOUT', severity: 'error' } as any })
    expect(serverReason(timeout)).toBe('Timed out')
    const noArgs = srv({ status: 'error', command: 'npx', last_error: 'exit 1', diagnostic: { code: 'MCPX_CONFIG_INVALID_COMMAND', severity: 'error', user_message: 'npx needs a package to run' } as any })
    expect(serverReason(noArgs)).toBe('npx needs a package to run')
  })

  it('token refresh pending is not "Sign-in expired"', () => {
    const s = srv({ status: 'connecting', summary: 'Token refresh pending', health: undefined } as any)
    s.health = { level: 'degraded', admin_state: 'enabled', summary: 'Token refresh pending', detail: 'access token expired; refresh retrying', status: 'connecting', usable: false, actions: ['view_logs'] } as any
    expect(serverReason(s)).toBe('Token refresh pending')
  })

  it('the diagnostic message leads the summary (FR-022), the summary leads the raw error', () => {
    const s = srv({ status: 'error', summary: 'Access forbidden', last_error: 'HTTP 403', diagnostic: { code: 'MCPX_HTTP_403', severity: 'error', user_message: 'The server returned 403 Forbidden' } as any })
    expect(serverReason(s)).toBe('The server returned 403 Forbidden')
  })

  it('a health payload with neither status nor summary never reads Connected when unusable', () => {
    const s = { name: 'x', protocol: 'stdio', enabled: true, quarantined: false, connected: true, connecting: false, tool_count: 0, health: { level: 'unhealthy', admin_state: 'enabled', summary: '', usable: false, actions: [] } } as any
    expect(serverReason(s)).toBe('Unavailable')
  })

  it('the real stdio-exit string maps to a short sentence, by code and by text alone', () => {
    const msg = 'The stdio server process exited before completing the MCP initialize handshake.'
    const byCode = srv({ status: 'needs_review', last_error: 'failed to connect: stdio transport: transport closed', diagnostic: { code: 'MCPX_STDIO_EXIT_BEFORE_INITIALIZE', severity: 'error', user_message: msg } as any })
    ;(byCode.health as any).admin_state = 'quarantined'
    expect(serverReason(byCode)).toBe('Server exited during startup')
    expect(serverReason(srv({ status: 'error', diagnostic: { code: 'MCPX_UNKNOWN_UNCLASSIFIED', severity: 'error', user_message: msg } as any }))).toBe('Server exited during startup')
  })

  it('"executable file not found" names the command, from the config or from the exec: "name" in the error', () => {
    const err = 'failed to connect: exec: "uvx": executable file not found in $PATH'
    expect(serverReason(srv({ status: 'error', command: 'uvx', last_error: err }))).toBe('Command not found: uvx')
    expect(serverReason(srv({ status: 'error', last_error: err }))).toBe('Command not found: uvx')
  })

  it('command not found without a command says only that', () => {
    expect(serverReason(srv({ status: 'error', last_error: 'executable file not found' }))).toBe('Command not found')
  })

  it('pattern by diagnostic code', () => {
    const s = srv({ status: 'error', diagnostic: { code: 'MCPX_HTTP_CONN_REFUSED', severity: 'error', user_message: 'Something long and different' } as any })
    expect(serverReason(s)).toBe('Connection refused')
  })

  it('enabled: the core summary leads, as the card did', () => {
    const s = srv({ status: 'error', summary: 'Host not found', last_error: 'dial tcp: lookup x: no such host' })
    expect(serverReason(s)).toBe('Host not found')
  })

  it('structured diagnostic message beats the last error', () => {
    const s = srv({ status: 'error', last_error: 'a: b: weird thing', diagnostic: { code: 'MCPX_X', severity: 'error', user_message: 'The server said no' } as any })
    expect(serverReason(s)).toBe('The server said no')
  })

  it('short last-error cause is the last segment of the chain', () => {
    expect(serverReason(srv({ status: 'error', last_error: 'connect: dial: strange failure' }))).toBe('strange failure')
  })

  it('collapses whitespace to one line', () => {
    expect(serverReason(srv({ status: 'error', last_error: 'bad\n thing\n  here' }))).toBe('bad thing here')
  })

  it('falls to health.summary, then the status label', () => {
    expect(serverReason(srv({ status: 'needs_review', summary: 'Quarantined - review tools' }))).toBe('Quarantined - review tools')
    expect(serverReason(srv({ status: 'sign_in_required' }))).toBe('Sign-in required')
  })

  it('an old error on a ready server is not its reason', () => {
    expect(serverReason(srv({ status: 'ready', summary: 'Connected (3 tools)', last_error: 'connection refused' }))).toBe('Connected (3 tools)')
  })

  it('quarantined: the admin-state summary does not hide the root cause', () => {
    const s = srv({ status: 'needs_review', summary: 'Quarantined for review', last_error: 'a: b: transport closed' })
    ;(s.health as any).admin_state = 'quarantined'
    expect(serverReason(s)).toBe('transport closed')
  })

  it('unknown status without summary prints the raw value, never invents', () => {
    expect(serverReason(srv({ status: 'frobnicated' }))).toBe('frobnicated')
  })

  it('old core: legacy words', () => {
    expect(serverReason(srv({ connected: true, last_error: undefined }))).toBe('Connected')
    expect(serverReason(srv({ connecting: true }))).toBe('Connecting')
    expect(serverReason(srv({}))).toBe('Disconnected')
  })
})
