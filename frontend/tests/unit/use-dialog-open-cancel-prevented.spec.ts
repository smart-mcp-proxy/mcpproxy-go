import { describe, it, expect } from 'vitest'
import { defineComponent, h, ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { useDialogOpen } from '@/composables/useDialogOpen'

/**
 * Review round 7, finding 1 (Spec 109 FR-055 follow-up): a `showModal()`
 * dialog's own Escape handling fires a `cancel` event and, unless it is
 * canceled, closes the dialog natively — independently of whatever a
 * `keydown` listener did with `preventDefault()`. `useDialogOpen` must always
 * cancel that native event so a non-dismissable dialog (canClose=false, as in
 * AuthErrorModal) cannot be Escaped away out from under `isOpen()`, and so
 * every dismissal is funneled through the one `close()` callback that owns
 * the "is this allowed right now" decision.
 *
 * jsdom does not implement the platform's own Escape-to-cancel algorithm, so
 * this dispatches the `cancel` event directly on the element — exactly the
 * event a real browser fires — to exercise the listener under test.
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

function harness(onClose: () => void) {
  const show = ref(false)

  const Modal = defineComponent({
    setup() {
      const { dialogEl } = useDialogOpen(() => show.value, onClose)
      return () =>
        h('dialog', {
          ref: (el: Element | null) => {
            if (el) polyfillNativeDialog(el as HTMLDialogElement)
            dialogEl.value = el as HTMLDialogElement | null
          },
        })
    },
  })

  return { show, Modal }
}

describe('useDialogOpen cancel prevention (Spec 109 PR-a review round 7, finding 1)', () => {
  it('prevents the native cancel event unconditionally', async () => {
    const { show, Modal } = harness(() => {
      show.value = false
    })
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement

    const cancelEvent = new Event('cancel', { cancelable: true })
    el.dispatchEvent(cancelEvent)

    expect(cancelEvent.defaultPrevented).toBe(true)
    wrapper.unmount()
  })

  it('keeps a non-dismissable dialog open when Escape is refused by close()', async () => {
    // Mirrors AuthErrorModal's handleClose: a no-op while canClose is false.
    const canClose = ref(false)
    const { show, Modal } = harness(() => {
      if (canClose.value) show.value = false
    })
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement
    expect(el.open).toBe(true)

    // The native cancel event fires on Escape regardless of what any keydown
    // listener did; our handler must cancel it so `el.close()` never runs.
    el.dispatchEvent(new Event('cancel', { cancelable: true }))
    await flushPromises()

    expect(el.open).toBe(true)
    expect(show.value).toBe(true)
    wrapper.unmount()
  })

  it('still lets a dismissable dialog close via the normal Escape/keydown path', async () => {
    const { show, Modal } = harness(() => {
      show.value = false
    })
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement
    expect(el.open).toBe(true)

    // The native cancel is still prevented (see first test), but the
    // keydown-driven close() (simulated directly here) flips isOpen(), and
    // the reactive watch performs the real, permitted el.close().
    el.dispatchEvent(new Event('cancel', { cancelable: true }))
    show.value = false
    await flushPromises()

    expect(el.open).toBe(false)
  })

  it('closes on Escape with no separate keydown listener at all (review round 8, finding 2)', async () => {
    // Every test above pairs useDialogOpen with a keydown-like effect that
    // flips `show` itself (mirroring useModalA11y's document keydown
    // listener, which is what actually closes the ~3 dialogs that pair both
    // composables). Most of this PR's dialogs (ConnectModal,
    // OnboardingWizard, Repositories.vue's three dialogs, teams/UserActivity,
    // UserServers, UserTokens) use useDialogOpen alone, with no keydown
    // listener anywhere to call close() — so unless the native `cancel`
    // event itself can trigger the close, Escape is a dead key on them. This
    // dispatches ONLY the native `cancel` event, the same one a real browser
    // fires on Escape, and nothing else, so it cannot pass by accident the
    // way flipping `show.value` directly would.
    const { show, Modal } = harness(() => {
      show.value = false
    })
    const wrapper = mount(Modal, { attachTo: document.body })
    show.value = true
    await flushPromises()
    const el = wrapper.element as HTMLDialogElement
    expect(el.open).toBe(true)

    el.dispatchEvent(new Event('cancel', { cancelable: true }))
    await flushPromises()

    expect(show.value).toBe(false)
    expect(el.open).toBe(false)
    wrapper.unmount()
  })
})
