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
})