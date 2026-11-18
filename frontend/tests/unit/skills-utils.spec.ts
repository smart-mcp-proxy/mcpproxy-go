import { describe, expect, it, vi } from 'vitest'
import {
  skillStateChip, skillFileDiff, lineDiff, skillExpectedHashes, readWholeSkillFile,
  skillsTabVisible, skillErrorNotice, SKILL_CONTENT_CHANGED, LINE_DIFF_CONTEXT, LINE_DIFF_MAX_OPS,
} from '@/utils/skills'

const file = (path: string, digest: string, size = 10, text = true) => ({ path, size, digest, text })

describe('skillStateChip', () => {
  const entry = (status: string, blocked = false) => ({ status, blocked }) as any
  it('ranks blocked > unusable > changed > pending > approved', () => {
    expect(skillStateChip(entry('approved', true)).label).toBe('Blocked')
    expect(skillStateChip(entry('unusable')).label).toBe('Unusable')
    expect(skillStateChip(entry('changed')).label).toBe('Changed')
    expect(skillStateChip(entry('pending')).label).toBe('Awaiting review')
    expect(skillStateChip(entry('approved')).label).toBe('Approved')
  })
  it('an unknown status is shown as is, in a neutral tone', () => {
    expect(skillStateChip(entry('weird'))).toEqual({ label: 'weird', tone: 'neutral' })
  })
})

describe('skillFileDiff', () => {
  it('classifies added, removed, changed and same files, sorted by path', () => {
    const rows = skillFileDiff(
      [file('b.md', 'd1'), file('gone.txt', 'x'), file('same.md', 's')],
      [file('same.md', 's'), file('b.md', 'd2'), file('new.txt', 'n')],
    )
    expect(rows.map(r => [r.path, r.state])).toEqual([
      ['b.md', 'changed'], ['gone.txt', 'removed'], ['new.txt', 'added'], ['same.md', 'same'],
    ])
    expect(rows[0].before?.digest).toBe('d1')
    expect(rows[0].after?.digest).toBe('d2')
  })
  it('without an approved copy every file is added', () => {
    const rows = skillFileDiff(undefined, [file('SKILL.md', 'a')])
    expect(rows).toEqual([{ path: 'SKILL.md', state: 'added', after: file('SKILL.md', 'a') }])
  })
})

describe('lineDiff', () => {
  it('reports same, added and deleted lines', () => {
    const r = lineDiff('a\nb\nc', 'a\nc\nd')
    expect(r.tooLarge).toBe(false)
    expect(r.ops.map(o => `${o.op}:${o.text}`)).toEqual(['same:a', 'del:b', 'same:c', 'add:d'])
  })
  it('identical text is all same lines', () => {
    expect(lineDiff('x\ny', 'x\ny').ops.every(o => o.op === 'same')).toBe(true)
  })
  it('falls back to too-large past the cell cap instead of building the matrix', () => {
    const a = Array.from({ length: 60 }, (_, i) => `a${i}`).join('\n')
    const b = Array.from({ length: 60 }, (_, i) => `b${i}`).join('\n')
    const r = lineDiff(a, b, 100)
    expect(r.tooLarge).toBe(true)
    expect(r.ops).toEqual([])
  })
  // Round-3 finding 6: the output is bounded, not only the LCS middle.
  it('collapses a huge shared prefix and suffix around a one-line change', () => {
    const shared = Array.from({ length: 130_000 }, (_, i) => `line ${i}`)
    const a = [...shared, 'old', ...shared].join('\n')
    const b = [...shared, 'new', ...shared].join('\n')
    const r = lineDiff(a, b)
    expect(r.tooLarge).toBe(false)
    expect(r.ops.length).toBeLessThan(20)
    const ops = r.ops.map(o => `${o.op}:${o.text}`)
    expect(ops).toContain('del:old')
    expect(ops).toContain('add:new')
    expect(ops[0]).toBe(`skip:${130_000 - LINE_DIFF_CONTEXT} unchanged lines`)
    expect(ops[ops.length - 1]).toBe(`skip:${130_000 - LINE_DIFF_CONTEXT} unchanged lines`)
    expect(ops).toContain(`same:line ${130_000 - 1}`)
    expect(ops).not.toContain(`same:line ${130_000 - 1 - LINE_DIFF_CONTEXT}`)
  })
  it('collapses an unchanged run inside the middle, and keeps a short one', () => {
    const run = Array.from({ length: 50 }, (_, i) => `m${i}`)
    const r = lineDiff(['x', ...run, 'y', 'k1', 'k2'].join('\n'), ['X', ...run, 'Y', 'k1', 'k2'].join('\n'))
    const ops = r.ops.map(o => `${o.op}:${o.text}`)
    expect(ops).toContain(`skip:${50 - 2 * LINE_DIFF_CONTEXT} unchanged lines`)
    expect(ops.slice(-2)).toEqual(['same:k1', 'same:k2'])
  })
  it('is too large when the changed lines alone pass the op limit', () => {
    const a = Array.from({ length: 10 }, (_, i) => `a${i}`).join('\n')
    const b = Array.from({ length: 30_000 }, (_, i) => `b${i}`).join('\n')
    const r = lineDiff(a, b)
    expect(r.tooLarge).toBe(true)
    expect(r.ops).toEqual([])
    expect(LINE_DIFF_MAX_OPS).toBeLessThan(30_000)
  })
})

describe('skillExpectedHashes', () => {
  it('maps path to current_hash with no prototype, so a __proto__ path stays an own key', () => {
    const h = skillExpectedHashes([{ path: '__proto__', current_hash: 'sk1-a' }, { path: 'ok', current_hash: 'sk1-b' }])
    expect(Object.getPrototypeOf(h)).toBeNull()
    expect(JSON.parse(JSON.stringify(h))).toEqual(JSON.parse('{"__proto__":"sk1-a","ok":"sk1-b"}'))
    expect(Object.keys(h)).toEqual(['__proto__', 'ok'])
  })
})

describe('readWholeSkillFile', () => {
  it('joins the parts by next_offset', async () => {
    const parts: Record<number, any> = {
      0: { text: 'ab', offset: 0, next_offset: 2, size: 5, digest: 'd' },
      2: { text: 'cd', offset: 2, next_offset: 4, size: 5, digest: 'd' },
      4: { text: 'e', offset: 4, size: 5, digest: 'd' },
    }
    const fetchPart = vi.fn(async (o: number) => ({ success: true, data: parts[o] }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r).toMatchObject({ success: true, text: 'abcde', capped: false })
  })
  it('refuses a part of another version (digest moved between parts) and keeps no text', async () => {
    const parts: Record<number, any> = {
      0: { text: 'ab', offset: 0, next_offset: 2, size: 5, digest: 'd' },
      2: { text: 'XY', offset: 2, next_offset: 4, size: 5, digest: 'd-new' },
    }
    const fetchPart = vi.fn(async (o: number) => ({ success: true, data: parts[o] }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r).toMatchObject({ success: false, errorCode: SKILL_CONTENT_CHANGED })
    expect((r as any).text).toBeUndefined()
    expect((r as any).error).toMatch(/This file changed; reload to review the new version/)
  })
  it('refuses a first part whose digest is not the one listed on screen', async () => {
    const fetchPart = vi.fn(async () => ({ success: true, data: { text: 'ab', offset: 0, size: 2, digest: 'other' } }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r).toMatchObject({ success: false, errorCode: SKILL_CONTENT_CHANGED })
  })
  it('refuses a part that answers another offset', async () => {
    const parts: Record<number, any> = {
      0: { text: 'ab', offset: 0, next_offset: 2, size: 5, digest: 'd' },
      2: { text: 'b', offset: 1, size: 5, digest: 'd' },
    }
    const fetchPart = vi.fn(async (o: number) => ({ success: true, data: parts[o] }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r).toMatchObject({ success: false, errorCode: SKILL_CONTENT_CHANGED })
  })
  it('stops at the part cap and says so', async () => {
    const fetchPart = vi.fn(async (o: number) => ({ success: true, data: { text: 'x', offset: o, next_offset: o + 1, size: 99, digest: 'd' } }))
    const r = await readWholeSkillFile(fetchPart as any, 'd', 3)
    expect(r).toMatchObject({ success: true, text: 'xxx', capped: true })
    expect(fetchPart).toHaveBeenCalledTimes(3)
  })
  it('refuses a next_offset that does not advance', async () => {
    const fetchPart = vi.fn(async () => ({ success: true, data: { text: 'x', offset: 0, next_offset: 0, size: 9, digest: 'd' } }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r.success).toBe(false)
  })
  it('passes a failing part through', async () => {
    const fetchPart = vi.fn(async () => ({ success: false, error: 'boom', errorCode: 'stored_copy_damaged' }))
    const r = await readWholeSkillFile(fetchPart as any, 'd')
    expect(r).toMatchObject({ success: false, error: 'boom', errorCode: 'stored_copy_damaged' })
  })
})

describe('skillsTabVisible', () => {
  it('needs the feature on and either support or a record', () => {
    expect(skillsTabVisible(undefined)).toBe(false)
    expect(skillsTabVisible({ enabled: false, supported: true, skills: [] } as any)).toBe(false)
    expect(skillsTabVisible({ enabled: true, supported: false, skills: [] } as any)).toBe(false)
    expect(skillsTabVisible({ enabled: true, supported: true, skills: [] } as any)).toBe(true)
    expect(skillsTabVisible({ enabled: true, supported: false, skills: [{}] } as any)).toBe(true)
  })

  // Round-2 finding 11: the agent-token shape has no `supported` (and no scan).
  it('treats a listing without `supported` (the agent shape) as not offered', () => {
    expect(skillsTabVisible({ enabled: true, skills: [{ path: 'a', status: 'approved' }] } as any)).toBe(false)
    expect(skillsTabVisible({ enabled: true, skills: [] } as any)).toBe(false)
  })
})

describe('skillErrorNotice', () => {
  it('names the stale-review and replaced-server cases and passes the rest through', () => {
    expect(skillErrorNotice('review_out_of_date', 'x')).toMatch(/changed since you opened it/i)
    expect(skillErrorNotice('server_replaced', 'x')).toMatch(/removed and added again/i)
    // The refresh codes the backend sends (internal/httpapi/skills.go).
    expect(skillErrorNotice('refresh_superseded', 'x')).toMatch(/reconnected during the refresh/i)
    expect(skillErrorNotice('skills_not_supported', 'x')).toMatch(/does not announce the skills extension/i)
    expect(skillErrorNotice('skills_disabled', 'x')).toMatch(/turned off/i)
    expect(skillErrorNotice('skill_unusable', 'cannot approve: bad')).toBe('cannot approve: bad')
    expect(skillErrorNotice(undefined, 'plain')).toBe('plain')
  })
})
