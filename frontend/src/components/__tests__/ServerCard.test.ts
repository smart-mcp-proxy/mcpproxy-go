import { describe, it, expect, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ServerCard from '../ServerCard.vue'

describe('ServerCard', () => {
  let router: any
  let pinia: any

  beforeEach(() => {
    // Setup Pinia
    pinia = createPinia()
    setActivePinia(pinia)

    // Setup Router
    router = createRouter({
      history: createWebHistory(),
      routes: [{ path: '/', component: { template: '<div>Home</div>' } }]
    })
  })

  it('renders server information correctly', () => {
    const server = {
      name: 'test-server',
      protocol: 'http' as const,
      enabled: true,
      connected: true,
      url: 'https://api.example.com',
      tool_count: 5
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: {
        plugins: [pinia, router]
      }
    })

    expect(wrapper.text()).toContain('test-server')
    expect(wrapper.text()).toContain('5')
    expect(wrapper.find('.badge-success')).toBeTruthy()
  })

  it('shows correct status for disabled server', () => {
    const server = {
      name: 'disabled-server',
      protocol: 'stdio' as const,
      enabled: false,
      connected: false,
      tool_count: 0
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: {
        plugins: [pinia, router]
      }
    })

    expect(wrapper.text()).toContain('disabled-server')
    expect(wrapper.text()).toContain('Disabled')
  })

  it('shows tool-quarantine banner with Review link when tools are pending and server is not quarantined', () => {
    const server = {
      name: 'partial-server',
      protocol: 'stdio' as const,
      enabled: true,
      connected: true,
      quarantined: false,
      tool_count: 10,
      quarantine: { pending_count: 3, changed_count: 0, blocked_count: 0 }
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: { plugins: [pinia, router] }
    })

    expect(wrapper.text()).toContain('3 of 10 tools pending security approval')
    const review = wrapper.find('a.btn-warning')
    expect(review.exists()).toBe(true)
    expect(review.attributes('href')).toBe('/servers/partial-server?tab=tools')
    // The server-level "Server is quarantined" banner must NOT render here
    expect(wrapper.text()).not.toContain('Server is quarantined')
  })

  it('says "All N pending" when every tool is pending', () => {
    const server = {
      name: 'fully-pending',
      protocol: 'stdio' as const,
      enabled: true,
      connected: true,
      quarantined: false,
      tool_count: 4,
      quarantine: { pending_count: 4, changed_count: 0, blocked_count: 0 }
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: { plugins: [pinia, router] }
    })

    expect(wrapper.text()).toContain('All 4 tools pending security approval')
  })

  it('flags rug-pull-style changed tools separately', () => {
    const server = {
      name: 'rugpull',
      protocol: 'stdio' as const,
      enabled: true,
      connected: true,
      quarantined: false,
      tool_count: 5,
      quarantine: { pending_count: 0, changed_count: 2, blocked_count: 0 }
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: { plugins: [pinia, router] }
    })

    expect(wrapper.text()).toContain('2 tools changed since approval')
  })

  it('does not double up: server-level banner wins over tool-level banner', () => {
    const server = {
      name: 'srv-quarantined',
      protocol: 'stdio' as const,
      enabled: true,
      connected: false,
      quarantined: true,
      tool_count: 4,
      quarantine: { pending_count: 4, changed_count: 0, blocked_count: 0 }
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: { plugins: [pinia, router] }
    })

    expect(wrapper.text()).toContain('Server is quarantined')
    expect(wrapper.text()).not.toContain('pending security approval')
  })

  it('shows both disabled and pending counts when both apply', () => {
    const server = {
      name: 'mixed',
      protocol: 'stdio' as const,
      enabled: true,
      connected: true,
      quarantined: false,
      tool_count: 10,
      quarantine: { pending_count: 2, changed_count: 0, blocked_count: 3 }
    }

    const wrapper = mount(ServerCard, {
      props: { server },
      global: { plugins: [pinia, router] }
    })

    expect(wrapper.text()).toContain('3 disabled')
    expect(wrapper.text()).toContain('2 pending approval')
  })
})
