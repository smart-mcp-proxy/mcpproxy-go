import { describe, it, expect } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { useDialogOpen } from '@/composables/useDialogOpen'

/**
 * jsdom implements the `open` attribute reflection on <dialog> but not
 * `showModal()`/`close()` (no layout, no top layer), so useDialogOpen falls
 * back to toggling the `open` attribute directly in every other unit test —
 * the real, browser-only code path (the one the H4 stacking fix depends on)
 * is otherwise untested. These tests polyfill just enough of the native
 * behaviour — `showModal()` sets `.open`, `close()` clears it and fires the
 * `close` event synchronously, exactly like a real dialog dismissed via the
 * Escape key — to exercise that path.
 */
function polyfillNativeDialog(el: HTMLDialogElement) {
  el.showModal = function (this: HTMLDialogElement) {
    this.open = true
  }
  el.close = function (this: HTMLDialogElement) {
    if (!this.open) return
    this.open = false
    this.dispatchEvent(new Event('close'))
  }
}

function harness() {
  const show = ref(false)
  const onCloseCalls = ref(0)

  const Modal = defineComponent({
    setup() {
      const { dialogEl } = useDialogOpen(
        () => show.value,
        () => {
          onCloseCalls.value++
          show.value = false
        }
      )
      return () =>
        h('dialog', {
          ref: (el: Element | null) => {
            if (el) polyfillNativeDialog(el as HTMLDialogElement)
            dialogEl.value = el as HTMLDialogElement | null
          },
        })
    },
  })

  return { show, onCloseCalls, Modal }
}

describe('useDialogOpen native close sync (Spec 109 PR-a review round 1)', () => {
  it('syncs external state back to closed when the dialog closes natively (Escape)', async () => {
    const { show, onCloseCalls, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement
    expect(el.open).toBe(true)

    // Nothing in Vue-land triggered this — the browser closed the dialog on
    // its own (Escape / native cancel), exactly as showModal() promises.
    el.close()
    await flushPromises()

    expect(onCloseCalls.value).toBe(1)
    expect(show.value).toBe(false)
    wrapper.unmount()
  })

  it('does not stay bricked: reopens after a native close', async () => {
    const { show, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement

    el.close()
    await flushPromises()
    expect(show.value).toBe(false)

    show.value = true
    await flushPromises()
    expect(el.open).toBe(true)
    wrapper.unmount()
  })

  it('does not call onClose when the close was initiated from Vue state', async () => {
    const { show, onCloseCalls, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()

    show.value = false
    await flushPromises()

    expect(onCloseCalls.value).toBe(0)
    wrapper.unmount()
  })
})
