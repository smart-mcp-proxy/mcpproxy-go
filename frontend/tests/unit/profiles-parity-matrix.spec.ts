import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import realRouter from '@/router'

// Spec 108-l (FR-051, L4, T121): the Web UI half of the parity-matrix check.
// Every `route:<name>` the checked-in specs/108-profiles-v3/parity-matrix.json
// names for a Web cell must resolve, through the real router, to a named route
// that is not the catch-all. (testid:, component: and symbol: ids are resolved
// against source by TestProfilesV3ParityMatrixResolves in Go; this suite owns
// only the router.)

interface Cell { status: string; ids: string[]; tests: string[] }
interface Matrix { rows: Array<{ row: number; cells: Record<string, Cell> }> }

const matrix: Matrix = JSON.parse(readFileSync(resolve(__dirname, '../../../specs/108-profiles-v3/parity-matrix.json'), 'utf8'))

function routeIds(): Array<{ row: number; id: string }> {
  return matrix.rows.flatMap(row =>
    (row.cells.web?.ids ?? []).filter(id => id.startsWith('route:')).map(id => ({ row: row.row, id })),
  )
}

// A named route that is not the catch-all. A redirect route (tokens, sessions)
// counts: it resolves, and the destination is checked by navigation-redirects.
function resolvesToARealRoute(name: string): boolean {
  if (!realRouter.hasRoute(name) || name === 'not-found') return false
  const resolved = realRouter.resolve({ name })
  return resolved.matched.length > 0 && resolved.name !== 'not-found'
}

describe('parity matrix: Web routes resolve (FR-051)', () => {
  it('names the routes of the Web cells', () => {
    const names = routeIds().map(({ id }) => id.slice('route:'.length))
    expect(names.length).toBeGreaterThanOrEqual(8)
    // rows 1, 2, 7, 15, 16, 17, 18, 24 are reached by route
    for (const expected of ['profiles', 'profile-editor', 'clients', 'activity', 'usage', 'sessions', 'tools', 'tokens']) {
      expect(names).toContain(expected)
    }
  })

  for (const { row, id } of routeIds()) {
    const name = id.slice('route:'.length)
    it(`row ${row}: ${id} resolves to a named route that is not the catch-all`, () => {
      const params = name === 'profile-editor' ? { name: 'work' } : undefined
      expect(realRouter.hasRoute(name), `no route named ${name}`).toBe(true)
      expect(name).not.toBe('not-found')
      const resolved = realRouter.resolve({ name, params })
      expect(resolved.matched.length).toBeGreaterThan(0)
      expect(resolved.name).not.toBe('not-found')
    })
  }

  it('the comparison can fail: an unknown route name is not a real route', () => {
    expect(resolvesToARealRoute('nope')).toBe(false)
    expect(resolvesToARealRoute('not-found')).toBe(false)
    expect(resolvesToARealRoute('clients')).toBe(true)
  })

  it('a path nobody registered resolves to the catch-all, never to a Web cell', () => {
    expect(realRouter.resolve('/definitely/not/a/page').name).toBe('not-found')
  })

  it('every Web route id carries a test', () => {
    for (const row of matrix.rows) {
      const cell = row.cells.web
      if (cell.ids.some(id => id.startsWith('route:'))) expect(cell.tests.length, `row ${row.row}`).toBeGreaterThan(0)
    }
  })
})
