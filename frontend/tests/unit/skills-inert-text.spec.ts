import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import SkillsPanel from '@/components/skills/SkillsPanel.vue'
import api from '@/services/api'
import { makeListing, makeSkill, skillFile, ok } from './fixtures/skills125'

vi.mock('@/services/api', () => ({ default: {
  getServerSkills: vi.fn(), refreshServerSkills: vi.fn(), getSkillContent: vi.fn(),
  approveSkills: vi.fn(), blockSkills: vi.fn(), unblockSkills: vi.fn(),
} }))

const evil = '<img src=x onerror=alert(1)>'

describe('skills text from the upstream stays inert', () => {
  it('renders names, descriptions, file names, file text, findings and reasons as text', async () => {
    ;(api.getServerSkills as any).mockResolvedValue(ok(makeListing([makeSkill('evil', {
      name: evil, description: evil, status: 'changed', approved_hash: 'x', unusable_reason: evil,
      files: [skillFile(`${evil}.md`)], approved_files: [skillFile(`${evil}.md`, 'other')],
      scan: { verdict: 'dangerous', complete: true, incomplete: '', findings_truncated: false,
        findings: [{ file: `${evil}.md`, offset: 0, rule: evil, severity: evil, evidence: evil }] },
    })])))
    ;(api.getSkillContent as any).mockResolvedValue(ok({ text: evil, offset: 0, size: 1, digest: `sha256:${evil}.md` }))
    const w = mount(SkillsPanel, { props: { serverName: 'srv' } })
    await flushPromises()
    await w.get('[data-test="skill-row-evil"]').trigger('click'); await flushPromises()
    await w.get(`[data-test="skill-view-${evil}.md"]`).trigger('click'); await flushPromises()
    expect(w.find('img').exists()).toBe(false)
    expect(w.text()).toContain(evil)
    expect(w.get('[data-test="skill-viewer-text"]').text()).toBe(evil)
  })

  it('no skills component uses v-html', () => {
    const dir = join(__dirname, '../../src/components/skills')
    for (const f of readdirSync(dir).filter(n => n.endsWith('.vue'))) {
      expect(readFileSync(join(dir, f), 'utf8')).not.toMatch(/v-html|innerHTML/)
    }
  })
})
