import { describe, it, expect } from 'vitest'
import { describeIsolation } from '../../src/utils/isolationState'
import type { Server } from '../../src/types/api'

const stdio = (over: Partial<Server> = {}): Server =>
  ({
    id: 'srv',
    name: 'srv',
    protocol: 'stdio',
    command: 'uvx',
    enabled: true,
    quarantined: false,
    connected: true,
    connecting: false,
    tool_count: 0,
    ...over,
  }) as Server

describe('describeIsolation', () => {
  // GH #1142: a server that inherits global isolation used to be reported as
  // enabled:false and therefore rendered as "not isolated".
  it('says isolated and names the inheritance when no override is set', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: true },
        isolation_effective: {
          mode: 'docker',
          isolated: true,
          inherited: true,
          global_mode: 'docker',
          source: 'global',
        },
      })
    )
    expect(d).not.toBeNull()
    expect(d!.isolated).toBe(true)
    expect(d!.label).toBe('Isolated (docker)')
    expect(d!.detail).toBe('Inherits the global setting (docker)')
  })

  it('names an explicit per-server opt-out', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: false, enabled_override: false },
        isolation_effective: {
          mode: 'none',
          isolated: false,
          inherited: false,
          global_mode: 'docker',
          source: 'server-opt-out',
        },
      })
    )
    expect(d!.isolated).toBe(false)
    expect(d!.label).toBe('Not isolated')
    expect(d!.detail).toBe('Turned off for this server (global setting is docker)')
  })

  it('names an explicit per-server opt-in', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: true, enabled_override: true },
        isolation_effective: {
          mode: 'docker',
          isolated: true,
          inherited: false,
          global_mode: 'docker',
          source: 'global',
        },
      })
    )
    expect(d!.detail).toBe('Turned on for this server')
  })

  it('warns when a per-server opt-in is being ignored', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: false, enabled_override: true },
        isolation_effective: {
          mode: 'none',
          isolated: false,
          inherited: false,
          global_mode: 'none',
          source: 'server-opt-in-ignored',
        },
      })
    )
    expect(d!.isolated).toBe(false)
    expect(d!.detail).toBe('Turned on for this server, but global isolation is off — the setting is ignored')
  })

  it('explains the structural gates', () => {
    const already = describeIsolation(
      stdio({
        command: 'docker',
        isolation_effective: { mode: 'none', isolated: false, inherited: true, source: 'already-docker' },
      })
    )
    expect(already!.detail).toBe('This server already runs Docker itself')

    const notStdio = describeIsolation(
      stdio({
        isolation_effective: { mode: 'none', isolated: false, inherited: true, source: 'not-stdio' },
      })
    )
    expect(notStdio!.detail).toBe('No local process to isolate')
  })

  it('reports a per-server mode override', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: true, mode_override: 'sandbox' },
        isolation_effective: {
          mode: 'sandbox',
          isolated: true,
          inherited: false,
          global_mode: 'docker',
          source: 'server-mode',
        },
      })
    )
    expect(d!.label).toBe('Isolated (sandbox)')
    expect(d!.detail).toBe('Mode set for this server: sandbox')
  })

  // Forward-compat: an unrecognized source must degrade to the inherit story,
  // never to a wrong claim.
  it('treats an unknown source as inherited', () => {
    const d = describeIsolation(
      stdio({
        isolation: { enabled: true },
        isolation_effective: {
          mode: 'docker',
          isolated: true,
          inherited: true,
          global_mode: 'docker',
          source: 'something-new',
        },
      })
    )
    expect(d!.detail).toBe('Inherits the global setting (docker)')
  })

  it('returns null when the backend sent no resolution block', () => {
    expect(describeIsolation(stdio())).toBeNull()
  })
})
