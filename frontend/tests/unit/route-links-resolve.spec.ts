import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'fs'
import { join, resolve } from 'path'

// Spec 134 FR-015 / SC-007: no link or button may lead to a route that renders
// the not-found page. Every `{ name: '<route>' }` used as a navigation target
// must be a registered route name.

const root = resolve(__dirname, '../../src')

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) walk(path, out)
    else if (/\.(vue|ts)$/.test(name)) out.push(path)
  }
  return out
}

describe('named navigation targets exist (spec 134 FR-015)', () => {
  const router = readFileSync(join(root, 'router/index.ts'), 'utf8')
  const registered = new Set([...router.matchAll(/\bname:\s*'([^']+)'/g)].map((m) => m[1]))

  it('every :to / router.push({ name }) names a registered route', () => {
    const missing: string[] = []
    for (const file of walk(root)) {
      if (file.endsWith('router/index.ts')) continue
      const source = readFileSync(file, 'utf8')
      const re = /(?::to="\{\s*name:\s*'([^']+)'|(?:push|replace)\(\{\s*name:\s*'([^']+)'|:to="\{\s*name:\s*"([^"]+)")/g
      for (const m of source.matchAll(re)) {
        const name = m[1] || m[2] || m[3]
        if (!registered.has(name)) missing.push(`${file.slice(root.length + 1)} -> ${name}`)
      }
    }
    expect(missing).toEqual([])
  })
})
