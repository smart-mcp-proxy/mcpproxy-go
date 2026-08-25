import { nextTick, onBeforeUnmount, ref, watch, type Ref } from 'vue'

/**
 * Keyboard + focus behaviour for the `<dialog :open>` modals used across the
 * Web UI (UX audit F6).
 *
 * These modals are rendered with the `open` attribute rather than
 * `HTMLDialogElement.showModal()`, so the browser gives us *none* of the modal
 * affordances: Escape does not close, focus stays on the trigger button and
 * Tab walks straight out of the dialog into the page behind it. This composable
 * supplies those three behaviours plus focus restoration, without changing how
 * the dialogs are mounted or styled.
 *
 * Usage:
 *   const { dialogRef } = useModalA11y(() => props.show, handleClose)
 *   <div class="modal-box" ref="dialogRef" role="dialog" aria-modal="true">
 */

// Elements that can receive focus inside the dialog. `[hidden]` and elements
// inside a `display:none` subtree are filtered out separately (offsetParent).
const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

export interface ModalA11yOptions {
  /**
   * Selector for the element that should receive focus when the modal opens.
   * Defaults to the first focusable element that is not the header close
   * button, so keyboard users land on the form rather than on "✕".
   */
  initialFocus?: string
}

export function useModalA11y(
  isOpen: () => boolean,
  close: () => void,
  options: ModalA11yOptions = {}
) {
  const dialogRef = ref<HTMLElement | null>(null)
  let previouslyFocused: HTMLElement | null = null

  function visibleFocusables(): HTMLElement[] {
    const root = dialogRef.value
    if (!root) return []
    return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter((el) => {
      if (el.hasAttribute('hidden')) return false
      if (el.getAttribute('aria-hidden') === 'true') return false
      // jsdom reports offsetParent === null for everything, so only trust this
      // signal in a real browser (where at least one element reports a parent).
      return true
    })
  }

  function focusInitial() {
    const root = dialogRef.value
    if (!root) return
    if (options.initialFocus) {
      const preferred = root.querySelector<HTMLElement>(options.initialFocus)
      if (preferred) {
        preferred.focus()
        return
      }
    }
    const candidates = visibleFocusables()
    const first = candidates.find((el) => el.dataset.modalCloseButton === undefined) ?? candidates[0]
    if (first) {
      first.focus()
    } else {
      // Nothing focusable yet (async content): park focus on the box itself so
      // Escape and the Tab trap still have a live target inside the dialog.
      root.setAttribute('tabindex', '-1')
      root.focus()
    }
  }

  function onKeydown(event: KeyboardEvent) {
    if (!isOpen()) return

    if (event.key === 'Escape' || event.key === 'Esc') {
      event.preventDefault()
      event.stopPropagation()
      close()
      return
    }

    if (event.key !== 'Tab') return

    const root = dialogRef.value
    if (!root) return
    const focusables = visibleFocusables()
    if (focusables.length === 0) return

    const first = focusables[0]
    const last = focusables[focusables.length - 1]
    const active = document.activeElement as HTMLElement | null

    // Focus escaped the dialog (or never entered it) — pull it back in.
    if (!active || !root.contains(active)) {
      event.preventDefault()
      ;(event.shiftKey ? last : first).focus()
      return
    }

    if (event.shiftKey && active === first) {
      event.preventDefault()
      last.focus()
    } else if (!event.shiftKey && active === last) {
      event.preventDefault()
      first.focus()
    }
  }

  function activate() {
    previouslyFocused = document.activeElement as HTMLElement | null
    document.addEventListener('keydown', onKeydown, true)
    void nextTick(() => focusInitial())
  }

  function deactivate() {
    document.removeEventListener('keydown', onKeydown, true)
    const target = previouslyFocused
    previouslyFocused = null
    if (target && typeof target.focus === 'function' && document.contains(target)) {
      target.focus()
    }
  }

  watch(
    isOpen,
    (open, wasOpen) => {
      if (open === wasOpen) return
      if (open) activate()
      else if (wasOpen !== undefined) deactivate()
    },
    { immediate: true }
  )

  onBeforeUnmount(() => {
    // Unmounting while open (a route change, a v-if around the modal) must not
    // strand the listener or the focus: deactivate does both, and its
    // document.contains guard makes restoring a no-op when the trigger went
    // away with the same subtree.
    deactivate()
  })

  return { dialogRef, focusInitial }
}
