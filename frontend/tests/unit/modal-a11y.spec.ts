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

    // The keydown listener lives on `document`, and stopPropagation does not
    // stop sibling listeners on the same node — so without a topmost check two
    // open modals would both close on one Escape.
    it('only the topmost modal reacts to Escape', async () => {
      const under = mount(AddServerModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()
      const over = mount(AddSecretModal, { props: { show: true }, attachTo: document.body })
      await flushPromises()

      pressEscape()
      expect(over.emitted('close'), 'the top modal closes').toBeTruthy()
      expect(under.emitted('close'), 'the one beneath it must not').toBeFalsy()

      // Once the top one is gone, the modal beneath owns Escape again.
      await over.setProps({ show: false })
      await flushPromises()
      pressEscape()
      expect(under.emitted('close')).toBeTruthy()

      over.unmount()
      under.unmount()
    })

    // Closing the modal underneath must leave focus where the top modal put it,
    // not drag it back to a trigger that now sits behind an open dialog.
    it('closing a modal underneath another does not steal focus from the top one', async () => {
      const underTrigger = document.createElement('button')
      underTrigger.textContent = 'under-trigger'
      document.body.appendChild(underTrigger)
      underTrigger.focus()

      const under = mount(AddServerModal, { props: { show: false }, attachTo: document.body })
      await under.setProps({ show: true })
      await flushPromises()

      const over = mount(AddSecretModal, { props: { show: false }, attachTo: document.body })
      await over.setProps({ show: true })
      await flushPromises()

      const topBox = over.find('[role="dialog"]').element
      expect(topBox.contains(document.activeElement)).toBe(true)

      // The one underneath closes on its own (e.g. its request resolved).
      await under.setProps({ show: false })
      await flushPromises()

      expect(document.activeElement).not.toBe(underTrigger)
      expect(topBox.contains(document.activeElement), 'focus stays in the top modal').toBe(true)

      over.unmount()
      under.unmount()
    })

    // The trigger lives outside the modal's own subtree here (the real shape:
    // a page-level button with the modal behind a v-if), so it survives the
    // unmount and must get focus back.
    it('restores focus to a surviving trigger when unmounted while still open', async () => {
      const trigger = document.createElement('button')
      document.body.appendChild(trigger)
      trigger.focus()

      const wrapper = mount(AddSecretModal, { props: { show: false }, attachTo: document.body })
      await wrapper.setProps({ show: true })
      await flushPromises()
      expect(document.activeElement).not.toBe(trigger)

      wrapper.unmount()
      await flushPromises() // the restore is deferred until after teardown
      expect(document.activeElement).toBe(trigger)

      trigger.remove()
    })

    // A trigger torn down together with the modal must not be focused on its
    // way out — that is a pointless focus/blur pair that scrolls and is
    // announced by a screen reader, for an element about to vanish.
    it('does not focus a trigger that is torn down with the modal', async () => {
      const host = document.createElement('div')
      const trigger = document.createElement('button')
      host.appendChild(trigger)
      document.body.appendChild(host)
      trigger.focus()

      const wrapper = mount(AddSecretModal, { props: { show: false }, attachTo: host })
      await wrapper.setProps({ show: true })
      await flushPromises()

      const focusSpy = vi.spyOn(trigger, 'focus')
      wrapper.unmount()
      host.remove() // the trigger's subtree goes away in the same teardown
      await flushPromises()

      expect(focusSpy).not.toHaveBeenCalled()
      focusSpy.mockRestore()
    })

    // The focus-on-open runs on nextTick; by then the modal may have closed
    // again, and grabbing focus would drag it somewhere invisible.
    it('does not grab focus if the modal closed before the open tick landed', async () => {
      const trigger = document.createElement('button')
      document.body.appendChild(trigger)
      trigger.focus()

      const wrapper = mount(AddSecretModal, { props: { show: false }, attachTo: document.body })
      // Open and close again within the same tick.
      await wrapper.setProps({ show: true })
      await wrapper.setProps({ show: false })
      await flushPromises()

      expect(document.activeElement).toBe(trigger)

      wrapper.unmount()
      trigger.remove()
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
