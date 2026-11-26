import { describe, it, expect } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { useDialogOpen } from '@/composables/useDialogOpen'

/**
 * A real `<dialog>.close()` clears `open` at once but fires its `close` event
 * later, as a queued task (HTML spec, "close the dialog"). If the dialog is
 * reopened in between (Escape, then Ctrl+K straight away), that late `close`
 * event belongs to the previous opening and must not close the new one.
 * This polyfill reproduces the browser's timing; the one in
 * use-dialog-open-native-close.spec.ts fires the event synchronously.
 */
function polyfillQueuedClose(el: HTMLDialogElement) {
  el.showModal = function (this: HTMLDialogElement) {
    this.open = true
  }
  el.close = function (this: HTMLDialogElement) {
    if (!this.open) return
    this.open = false
    setTimeout(() => this.dispatchEvent(new Event('close')), 0)
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
        },
      )
      return () =>
        h('dialog', {
          ref: (el: Element | null) => {
            if (el) polyfillQueuedClose(el as HTMLDialogElement)
            dialogEl.value = el as HTMLDialogElement | null
          },
        })
    },
  })
  return { show, onCloseCalls, Modal }
}

const nextTask = () => new Promise<void>(resolve => setTimeout(resolve, 0))

describe('useDialogOpen: a close event that arrives after a reopen', () => {
  it('keeps a dialog reopened before the previous close event fired', async () => {
    const { show, onCloseCalls, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement
    expect(el.open).toBe(true)

    show.value = false // Escape: state closes, the watcher calls el.close()
    await nextTick()
    expect(el.open).toBe(false)
    show.value = true // Ctrl+K before the queued close event has run
    await nextTick()
    expect(el.open).toBe(true)

    await nextTask() // the stale close event fires now
    await flushPromises()
    expect(onCloseCalls.value).toBe(0)
    expect(show.value).toBe(true)
    expect(el.open).toBe(true)
    wrapper.unmount()
  })

  it('still syncs state when the browser closes the dialog on its own', async () => {
    const { show, onCloseCalls, Modal } = harness()
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement

    el.close() // native dismissal, Vue state still says open
    await nextTask()
    await flushPromises()
    expect(onCloseCalls.value).toBe(1)
    expect(show.value).toBe(false)
    wrapper.unmount()
  })
})
