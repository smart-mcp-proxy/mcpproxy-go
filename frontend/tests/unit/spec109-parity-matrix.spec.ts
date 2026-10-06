import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import realRouter from '@/router'

// Spec 109-m (FR-091, T148b, M11): the Web UI half of the parity-matrix check.
// Every `route:<name>` the checked-in specs/109-ux-navigation-consistency/
// parity-matrix.json names for a Web cell must resolve, through the real
// router, to a named route that is not the catch-all. The other id kinds
// (component, symbol, testid) are resolved against source by
// TestSpec109ParityMatrixResolves in Go; this suite owns only the router.

interface Cell { status: string; ids: string[]; tests: string[] }
interface Matrix { rows: Array<{ row: string; cells: Record<string, Cell> }> }

const matrix: Matrix = JSON.parse(
  readFileSync(resolve(__dirname, '../../../specs/109-ux-navigation-consistency/parity-matrix.json'), 'utf8'),
)

function routeIds(): Array<{ row: string; id: string }> {
  return matrix.rows.flatMap(row =>
    (row.cells.web?.ids ?? []).filter(id => id.startsWith('route:')).map(id => ({ row: row.row, id })),
  )
}

// Fill every `:param` of a route's path with a placeholder so resolve() can build it.
function paramsFor(name: string): Record<string, string> {
  const record = realRouter.getRoutes().find(r => r.name === name)
  const params: Record<string, string> = {}
  for (const m of (record?.path ?? '').matchAll(/:(\w+)/g)) params[m[1]] = 'x'
  return params
}

function resolvesToARealRoute(name: string): boolean {
  if (!realRouter.hasRoute(name) || name === 'not-found') return false
  const resolved = realRouter.resolve({ name, params: paramsFor(name) })
  return resolved.matched.length > 0 && resolved.name !== 'not-found'
}

describe('spec 109 parity matrix: Web routes resolve (FR-091)', () => {
  it('names the routes of the Web cells', () => {
    const names = routeIds().map(({ id }) => id.slice('route:'.length))
    expect(names.length).toBeGreaterThanOrEqual(8)
    for (const expected of ['home', 'review', 'review-server', 'clients', 'tools', 'activity', 'usage', 'settings']) {
      expect(names).toContain(expected)
    }
  })

  for (const { row, id } of routeIds()) {
    const name = id.slice('route:'.length)
    it(`row ${row}: ${id} resolves to a named route that is not the catch-all`, () => {
      expect(realRouter.hasRoute(name), `no route named ${name}`).toBe(true)
      expect(name).not.toBe('not-found')
      expect(resolvesToARealRoute(name)).toBe(true)
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

  it('every ticked Web cell carries an identifier and a test; every dash carries a reason', () => {
    for (const row of matrix.rows) {
      const cell = row.cells.web as Cell & { reason?: string }
      if (cell.status === 'must') {
        expect(cell.ids.length, `row ${row.row} ids`).toBeGreaterThan(0)
        expect(cell.tests.length, `row ${row.row} tests`).toBeGreaterThan(0)
      }
      if (cell.status === 'out') expect(cell.reason, `row ${row.row} reason`).toBeTruthy()
    }
  })
})
