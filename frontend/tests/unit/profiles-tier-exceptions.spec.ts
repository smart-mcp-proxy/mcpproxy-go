import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ProfileCard from '@/components/profiles/ProfileCard.vue'
import { tierPhrase } from '@/utils/profiles'

// Spec 115 T065 (UI-003, G7): a cap with explicit allows above it names the
// exceptions; plain "Read-only" only when nothing above read is admitted.
describe('tierPhrase with exceptions', () => {
  it('read cap', () => {
    expect(tierPhrase('read', { write: 0, destructive: 0 })).toBe('Read-only')
    expect(tierPhrase('read', { write: 1, destructive: 0 })).toBe('Read-only + 1 write exception')
    expect(tierPhrase('read', { write: 2, destructive: 0 })).toBe('Read-only + 2 write exceptions')
    expect(tierPhrase('read', { write: 0, destructive: 1 })).toBe('Read-only + 1 destructive exception')
    expect(tierPhrase('read', { write: 1, destructive: 1 })).toBe('Read-only + 1 write + 1 destructive exceptions')
  })
  it('write cap', () => {
    expect(tierPhrase('write', { write: 3, destructive: 2 })).toBe('Read + write + 2 destructive exceptions')
    expect(tierPhrase('write', { write: 3, destructive: 0 })).toBe('Read + write')
  })
  it('without counts it keeps the old words', () => {
    expect(tierPhrase('read')).toBe('Read-only')
    expect(tierPhrase('destructive')).toBe('Everything')
    expect(tierPhrase(undefined)).toBe('')
  })
  it('ProfileCard shows the exception wording', () => {
    setActivePinia(createPinia())
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div/>' } }] })
    const wrapper = mount(ProfileCard, {
      props: { profile: { name: 'triage', max_tier: 'read', tool_counts: { read: 1, write: 1, destructive: 0, unannotated_hidden: 0 }, calls_24h: 0, blocked_24h: 0 } as any },
      global: { plugins: [router], stubs: { RouterLink: { template: '<a><slot/></a>' } } },
    })
    expect(wrapper.find('[data-test="profile-tier"]').text()).toContain('Read-only + 1 write exception')
  })
})
