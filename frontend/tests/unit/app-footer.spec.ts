import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AppFooter from '@/components/AppFooter.vue'
import { PROJECT_LINKS } from '@/config/links'
import { useSystemStore } from '@/stores/system'

// Spec 134 FR-014: below 768 px the project links and version stay reachable in
// a compact disclosure; at 768 px and wider the links stay in the footer row.

describe('page footer (spec 134 FR-014)', () => {
  it('keeps the four project links and the version in the wide row', () => {
    setActivePinia(createPinia())
    useSystemStore().$patch({ info: { version: 'v1.2.3' } as any })
    const wrapper = mount(AppFooter)
    const row = wrapper.get('[data-test="footer-links-wide"]')
    expect(row.findAll('a').map((a) => a.attributes('href'))).toEqual([
      PROJECT_LINKS.homepage, PROJECT_LINKS.github, PROJECT_LINKS.docs, PROJECT_LINKS.discussions,
    ])
    expect(row.text()).toContain('v1.2.3')
  })

  it('offers the same links and version in a keyboard-operable disclosure for phones', () => {
    setActivePinia(createPinia())
    useSystemStore().$patch({ info: { version: 'v1.2.3' } as any })
    const wrapper = mount(AppFooter)
    const details = wrapper.get('[data-test="footer-links-compact"]')
    expect(details.element.tagName).toBe('DETAILS')
    expect(details.get('summary').text()).toMatch(/MCPProxy/)
    expect(details.findAll('a').map((a) => a.attributes('href'))).toEqual([
      PROJECT_LINKS.homepage, PROJECT_LINKS.github, PROJECT_LINKS.docs, PROJECT_LINKS.discussions,
    ])
    expect(details.text()).toContain('v1.2.3')
    expect(details.classes().join(' ')).toMatch(/md:hidden/)
    expect(wrapper.get('[data-test="footer-links-wide"]').classes().join(' ')).toMatch(/hidden/)
  })
})
