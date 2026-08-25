import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AddServerModal from '@/components/AddServerModal.vue'
import AddSecretModal from '@/components/AddSecretModal.vue'

vi.mock('@/services/api', () => ({
  default: {
    getCanonicalConfigPaths: vi.fn().mockResolvedValue({ success: true, data: [] }),
    setSecret: vi.fn().mockResolvedValue({ success: true, data: { reference: '${keyring:x}' } }),
    previewImport: vi.fn(),
    importServers: vi.fn(),
  },
}))

function pressEscape() {
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
}

describe('modal accessibility (UX audit F6)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.body.innerHTML = ''
  })

  describe('AddServerModal', () => {
    it('carries the ARIA dialog contract', async () => {
      const wrapper = mount(AddServerModal, {
        props: { show: true },
        attachTo: document.body,
      })
      await flushPromises()

      const box = wrapper.find('[data-test="add-server-modal-box"]')
      expect(box.attributes('role')).toBe('dialog')
      expect(box.attributes('aria-modal')).toBe('true')
      const labelledBy = box.attributes('aria-labelledby')
      expect(labelledBy).toBeTruthy()
      expect(wrapper.find(`#${labelledBy}`).text()).toContain('Add New Server')

      wrapper.unmount()
    })

    it('keeps the submit row out of the scrolling body so it cannot fall below the fold', async () => {
      const wrapper = mount(AddServerModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      const body = wrapper.find('[data-test="add-server-modal-body"]')
      const footer = wrapper.find('[data-test="add-server-modal-footer"]')
      expect(body.exists()).toBe(true)
      expect(footer.exists()).toBe(true)
      // The scroll region is the body only; the footer is a sibling of it.
      expect(body.classes()).toContain('overflow-y-auto')
      expect(body.element.contains(footer.element)).toBe(false)
      expect(footer.text()).toContain('Add Server')

      wrapper.unmount()
    })

    it('has a header close affordance', async () => {
      const wrapper = mount(AddServerModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      const close = wrapper.find('[data-test="add-server-modal-close"]')
      expect(close.exists()).toBe(true)
      expect(close.attributes('aria-label')).toBe('Close')
      await close.trigger('click')
      expect(wrapper.emitted('close')).toBeTruthy()

      wrapper.unmount()
    })

    it('moves focus into the dialog when it opens', async () => {
      const trigger = document.createElement('button')
      document.body.appendChild(trigger)
      trigger.focus()
      expect(document.activeElement).toBe(trigger)

      const wrapper = mount(AddServerModal, { props: { show: false }, attachTo: document.body })
      await wrapper.setProps({ show: true })
      await flushPromises()

      const box = wrapper.find('[data-test="add-server-modal-box"]').element
      expect(box.contains(document.activeElement)).toBe(true)
      // The header ✕ is skipped — focus lands on the form, not on "close".
      expect((document.activeElement as HTMLElement).dataset.modalCloseButton).toBeUndefined()

      wrapper.unmount()
    })

    it('closes on Escape', async () => {
      const wrapper = mount(AddServerModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      pressEscape()
      expect(wrapper.emitted('close')).toBeTruthy()

      wrapper.unmount()
    })

    it('does not close on Escape while hidden', async () => {
      const wrapper = mount(AddServerModal, { props: { show: false }, attachTo: document.body })
      await flushPromises()

      pressEscape()
      expect(wrapper.emitted('close')).toBeFalsy()

      wrapper.unmount()
    })

    it('restores focus to the trigger when it closes', async () => {
      const trigger = document.createElement('button')
      document.body.appendChild(trigger)
      trigger.focus()

      const wrapper = mount(AddServerModal, { props: { show: false }, attachTo: document.body })
      await wrapper.setProps({ show: true })
      await flushPromises()
      expect(document.activeElement).not.toBe(trigger)

      await wrapper.setProps({ show: false })
      await flushPromises()
      expect(document.activeElement).toBe(trigger)

      wrapper.unmount()
    })

    it('traps Tab inside the dialog', async () => {
      const outside = document.createElement('button')
      document.body.appendChild(outside)

      const wrapper = mount(AddServerModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      const box = wrapper.find('[data-test="add-server-modal-box"]').element as HTMLElement
      // Focus escaped to a control behind the modal: Tab must pull it back in.
      outside.focus()
      const evt = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true })
      document.dispatchEvent(evt)
      expect(evt.defaultPrevented).toBe(true)
      expect(box.contains(document.activeElement)).toBe(true)

      wrapper.unmount()
    })
  })

  describe('AddSecretModal', () => {
    it('carries the ARIA dialog contract and closes on Escape', async () => {
      const wrapper = mount(AddSecretModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      const box = wrapper.find('[role="dialog"]')
      expect(box.exists()).toBe(true)
      expect(box.attributes('aria-modal')).toBe('true')

      pressEscape()
      expect(wrapper.emitted('close')).toBeTruthy()

      wrapper.unmount()
    })

    it('moves focus into the dialog on open', async () => {
      const wrapper = mount(AddSecretModal, { props: { show: false }, attachTo: document.body })
      await wrapper.setProps({ show: true })
      await flushPromises()

      const box = wrapper.find('[role="dialog"]').element
      expect(box.contains(document.activeElement)).toBe(true)

      wrapper.unmount()
    })
  })
})
