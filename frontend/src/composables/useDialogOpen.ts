import { onBeforeUnmount, ref, watch, type Ref } from 'vue'

/**
 * Drives a native `<dialog>` element's open state imperatively via
 * `showModal()` / `close()` instead of binding the `open` attribute (or a
 * `modal-open` CSS class) directly.
 *
 * Spec 109 FR-055 (H4): a `<dialog :open="x">` renders in the *normal* page
 * stacking context, so any ancestor establishing its own stacking context
 * (e.g. `position: sticky/fixed` + `z-index`) can paint over it regardless of
 * z-index value — that is how the sidebar's version block ended up on top of
 * the Add Server modal. `showModal()` promotes the dialog to the browser's
 * top layer, which always renders above every other element on the page, so
 * this stacking bug becomes structurally impossible rather than one more
 * z-index to keep in sync.
 *
 * jsdom (unit tests) does not implement `HTMLDialogElement.showModal`/`close`
 * (`typeof el.showModal !== 'function'`), so this falls back to toggling the
 * `open` attribute directly — the previous behaviour — keeping every existing
 * unit test that renders these dialogs working unchanged. Real browsers all
 * support `<dialog>` natively.
 *
 * A real `showModal()`-opened dialog also closes itself natively — Escape,
 * or a `<form method="dialog">` submit — without anything in Vue-land asking
 * it to. Left unhandled, that desyncs the driving `isOpen()` source from the
 * DOM: the dialog is closed but the source still reads `true`, so the next
 * click that would reopen it sets the same value again, the `watch` below
 * never fires, and `showModal()` never runs again — the dialog is bricked
 * until a full reload (review round 1, H4 follow-up). `onClose` is the
 * caller's hook to flip its own state back to closed when that happens; a
 * `close` event the dialog fired *because we just called `close()`
 * ourselves* is told apart by `isOpen()` already reading `false` by the time
 * it fires, so it does not loop back into another call to `onClose`.
 *
 * Escape is handled twice, on purpose, and the two paths do not agree by
 * accident — they are made to agree here. `useModalA11y`'s document-level
 * `keydown` capture listener calls `preventDefault()` on the keydown event
 * and then calls the caller's `close()` (which for a non-dismissable dialog,
 * e.g. AuthErrorModal while `canClose` is false, is a no-op). That
 * `preventDefault()` on `keydown` does NOT stop the browser's own
 * Escape-dismiss algorithm for a `showModal()` dialog: per the HTML spec,
 * pressing Escape on a modal `<dialog>` queues its own task to fire a
 * `cancel` event and, unless *that* event is canceled, calls `close()` —
 * independently of whatever the page's `keydown` listeners did. Left alone,
 * that means the dialog visually disappears on Escape even when the caller's
 * `close()` refused to close it (review round 7, finding 1): the native
 * element ends up `closed` while `isOpen()` still reads `true`, and — same
 * bricking shape as the H4 bug above — nothing reopens it. So every close
 * driven by the platform's own Escape handling is suppressed here
 * unconditionally, and routed back through the same `close()` the `keydown`
 * listener already calls: if that decides to close, it flips `isOpen()`,
 * and the `watch` below performs the actual (permitted) `el.close()`.
 */
export function useDialogOpen(isOpen: () => boolean, onClose?: () => void) {
  const dialogEl: Ref<HTMLDialogElement | null> = ref(null)

  function sync(open: boolean) {
    const el = dialogEl.value
    if (!el) return
    const supportsNative = typeof el.showModal === 'function' && typeof el.close === 'function'
    if (!supportsNative) {
      // Fallback for environments without <dialog> support (test DOM only).
      if (open) el.setAttribute('open', '')
      else el.removeAttribute('open')
      return
    }
    if (open && !el.open) {
      el.showModal()
    } else if (!open && el.open) {
      el.close()
    }
  }

  function handleNativeClose() {
    // isOpen() is still true only when nothing in Vue-land initiated this —
    // a genuine native dismissal that Vue's state does not know about yet.
    if (onClose && isOpen()) onClose()
  }

  function handleCancel(event: Event) {
    // Always block the browser's own Escape-triggered close. `useModalA11y`
    // already owns Escape (it must, to run the Tab trap / focus-stack logic
    // too) and calls the caller's `close()` on it; letting the platform also
    // act on the same keypress double-handles it and, for a dialog whose
    // `close()` is currently a no-op, closes it anyway (see comment above).
    event.preventDefault()
  }

  watch(isOpen, sync, { flush: 'post' })

  // Apply the initial state once the element has mounted (a plain immediate
  // watcher would run before the template ref is bound), and (re)attach the
  // native `close`/`cancel` listeners to whichever element is currently
  // bound.
  watch(
    dialogEl,
    (el, prevEl) => {
      prevEl?.removeEventListener('close', handleNativeClose)
      prevEl?.removeEventListener('cancel', handleCancel)
      if (el) {
        el.addEventListener('close', handleNativeClose)
        el.addEventListener('cancel', handleCancel)
        sync(isOpen())
      }
    },
    { flush: 'post' }
  )

  onBeforeUnmount(() => {
    const el = dialogEl.value
    if (!el) return
    el.removeEventListener('close', handleNativeClose)
    el.removeEventListener('cancel', handleCancel)
    if (el.open && typeof el.close === 'function') el.close()
  })

  return { dialogEl }
}
