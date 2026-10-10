import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ProfileToolTable from '@/components/profiles/ProfileToolTable.vue'

vi.mock('@/services/api', () => ({ default: { explainAccess: vi.fn().mockResolvedValue({ success: false, error: 'x' }), getProfiles: vi.fn().mockResolvedValue({ profiles: [] }), hasAPIKey: () => true } }))

// UX-07: the preview is policy-only; the labels say so and a held tool says why.
const ROWS = [
  { server: 'lib', tool: 'ok', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: true, reason: '' }, classification_stale: false },
  { server: 'lib', tool: 'pending', intrinsic_tier: 'read', profile_tier: 'read', access: { visible: true, callable: false, reason: 'tool_approval' }, classification_stale: false },
  { server: 'lib', tool: 'cap', intrinsic_tier: 'write', profile_tier: 'write', access: { visible: false, callable: false, reason: 'above_tier_cap' }, classification_stale: false },
]
function mountTable(counts: any, rows = ROWS) {
  setActivePinia(createPinia())
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/', component: { template: '<div/>' } }] })
  return mount(ProfileToolTable, {
    props: { rows, counts, draftRules: undefined, profileLabel: 'qa-read', profileName: 'qa-read', serversChosen: true, editable: false },
    global: { plugins: [router] },
  })
}
const t = (w: any, sel: string) => w.get(`[data-test="${sel}"]`).text().replace(/\s+/g, ' ')

describe('ProfileToolTable availability labels (UX-07)', () => {
  it('admin: separates allowed-by-profile, callable now and held, with the reason and Explain access', () => {
    const w = mountTable({ visible: 2, hidden: 1, callable: 1, by_reason: { tool_approval: 1, above_tier_cap: 1 } })
    expect(t(w, 'profile-tool-counts')).toBe('2 allowed by profile · 1 hidden · 1 callable now · 1 held')
    expect(t(w, 'profile-tool-state-lib__ok')).toBe('Callable now')
    expect(t(w, 'profile-tool-state-lib__pending')).toBe('Held')
    expect(t(w, 'profile-tool-reason-lib__pending')).toBe('Needs review')
    expect(t(w, 'profile-tool-state-lib__cap')).toBe('Hidden')
    expect(w.get('[data-test="profile-tool-explain-lib__pending"]').attributes('aria-label')).toBe('Explain access to lib:pending')
    expect(w.find('[data-test="profile-tool-explain-lib__ok"]').exists()).toBe(false)
  })

  it('admin filters: Callable now and Held', async () => {
    const w = mountTable({ visible: 2, hidden: 1, callable: 1 })
    await w.get('[data-test="tool-filter-reason"]').setValue('held')
    expect(w.findAll('tbody tr').map(r => r.attributes('data-test'))).toEqual(['profile-tool-row-lib__pending'])
    await w.get('[data-test="tool-filter-reason"]').setValue('callable')
    expect(w.findAll('tbody tr').map(r => r.attributes('data-test'))).toEqual(['profile-tool-row-lib__ok'])
    await w.get('[data-test="tool-filter-reason"]').setValue('visible')
    expect(w.findAll('tbody tr')).toHaveLength(2)
  })

  it('non-admin: no callable/held split, no held reason, no Explain access', () => {
    const w = mountTable({ visible: 2, hidden: 1 }, ROWS.filter(r => r.access.visible))
    expect(t(w, 'profile-tool-counts')).toBe('2 allowed by profile · 1 hidden')
    expect(t(w, 'profile-tool-state-lib__pending')).toBe('Allowed by profile')
    expect(w.find('[data-test="profile-tool-reason-lib__pending"]').exists()).toBe(false)
    expect(w.find('[data-test="profile-tool-explain-lib__pending"]').exists()).toBe(false)
    expect(w.find('option[value="held"]').exists()).toBe(false)
  })
})
