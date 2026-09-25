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
 */
export function useDialogOpen(isOpen: () => boolean) {
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

  watch(isOpen, sync, { flush: 'post' })

  // Apply the initial state once the element has mounted (a plain immediate
  // watcher would run before the template ref is bound).
  watch(
    dialogEl,
    (el) => {
      if (el) sync(isOpen())
    },
    { flush: 'post' }
  )

  onBeforeUnmount(() => {
    const el = dialogEl.value
    if (el?.open && typeof el.close === 'function') el.close()
  })

  return { dialogEl }
}
