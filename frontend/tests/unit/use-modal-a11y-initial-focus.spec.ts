import { describe, it, expect, beforeEach } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { useModalA11y } from '@/composables/useModalA11y'

/**
 * The `initialFocus` option has no caller in the app yet, so these tests are
 * the only thing holding its contract (UX audit F6, cross-model review round 5):
 * it must be able to target the dialog heading — programmatically focusable via
 * tabindex="-1" but deliberately outside the tab order, the standard ARIA
 * dialog pattern — while still refusing anything that cannot take focus.
 */
function harness(initialFocus?: string) {
  const show = ref(false)
  const closed = ref(0)

  const Modal = defineComponent({
    setup() {
      const { dialogRef } = useModalA11y(
        () => show.value,
        () => {
          closed.value++
          show.value = false
        },
        initialFocus ? { initialFocus } : {}
      )
      return () =>
        h('div', { ref: dialogRef, role: 'dialog' }, [
          h('h2', { id: 'title', tabindex: '-1' }, 'Dialog title'),
          h('button', { id: 'plain' }, 'plain'),
          h('button', { id: 'hidden-btn', style: 'display:none' }, 'hidden'),
          h('button', { id: 'disabled-btn', disabled: true }, 'disabled'),
        ])
    },
  })

  return { show, closed, Modal }
}

describe('useModalA11y initialFocus (UX audit F6)', () => {
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  it('can focus a tabindex="-1" heading, which is not in the tab order', async () => {
    const { show, Modal } = harness('#title')
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    expect((document.activeElement as HTMLElement)?.id).toBe('title')
    wrapper.unmount()
  })

  it('falls back to the first tabbable control when the selector matches nothing', async () => {
    const { show, Modal } = harness('#does-not-exist')
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    expect((document.activeElement as HTMLElement)?.id).toBe('plain')
    wrapper.unmount()
  })

  it('refuses a disabled target and falls back', async () => {
    const { show, Modal } = harness('#disabled-btn')
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    expect((document.activeElement as HTMLElement)?.id).not.toBe('disabled-btn')
    expect((document.activeElement as HTMLElement)?.id).toBe('plain')
    wrapper.unmount()
  })

  it('without the option, focuses the first tabbable control', async () => {
    const { show, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    expect((document.activeElement as HTMLElement)?.id).toBe('plain')
    wrapper.unmount()
  })

  it('a disabled control never bounds the Tab trap', async () => {
    const { show, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    // Cycle forward past the end; the trap must wrap to the first tabbable
    // control rather than parking on the disabled one.
    for (let i = 0; i < 6; i++) {
      document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }))
    }
    expect((document.activeElement as HTMLElement)?.id).not.toBe('disabled-btn')
    expect((document.activeElement as HTMLElement)?.id).not.toBe('hidden-btn')

    wrapper.unmount()
  })
})
