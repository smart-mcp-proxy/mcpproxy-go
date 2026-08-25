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

/**
 * Every currently-open modal, oldest first.
 *
 * The keydown listener lives on `document`, and stopPropagation does NOT stop
 * other listeners already bound to the same node in the same phase — so with
 * two modals open, both would see Escape and both would close. Only the
 * topmost entry acts; the ones beneath it ignore the key entirely.
 */
const openStack: symbol[] = []

function isTopmost(id: symbol): boolean {
  return openStack.length > 0 && openStack[openStack.length - 1] === id
}

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
  const id = Symbol('modal-a11y')
  let previouslyFocused: HTMLElement | null = null

  function visibleFocusables(): HTMLElement[] {
    const root = dialogRef.value
    if (!root) return []
    return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR)).filter((el) => {
      if (el.hasAttribute('hidden')) return false
      if (el.getAttribute('aria-hidden') === 'true') return false
      // A control inside a `v-show`-hidden (display:none) or `visibility:hidden`
      // subtree is in the DOM but cannot take focus, and letting it bound the
      // trap would make Tab appear to stick. checkVisibility covers display and
      // content-visibility on its own; `visibility` needs checkVisibilityCSS,
      // which is off by default. jsdom does not implement the method, so the
      // tests fall through to "visible" — which matches how they build the DOM
      // (v-if branches are absent rather than hidden).
      if (typeof el.checkVisibility === 'function') {
        return el.checkVisibility({ checkVisibilityCSS: true })
      }
      return true
    })
  }

  function focusInitial() {
    const root = dialogRef.value
    if (!root) return
    const candidates = visibleFocusables()
    if (options.initialFocus) {
      // Matched against the SAME filtered set, not a raw querySelector: a
      // configured selector must not be able to hand focus to a control that
      // is hidden or disabled, which would silently focus nothing.
      const selector = options.initialFocus
      const preferred = candidates.find((el) => el.matches(selector))
      if (preferred) {
        preferred.focus()
        return
      }
    }
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
    // A modal underneath another one neither closes on Escape nor fights the
    // top modal for focus.
    if (!isOpen() || !isTopmost(id)) return

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
    if (openStack.includes(id)) return
    previouslyFocused = document.activeElement as HTMLElement | null
    openStack.push(id)
    document.addEventListener('keydown', onKeydown, true)
    void nextTick(() => {
      // The tick may land after this modal closed again, or after a second
      // modal opened on top of it; in either case grabbing focus now would
      // take it somewhere the user cannot see.
      if (isOpen() && isTopmost(id)) focusInitial()
    })
  }

  function deactivate(deferRestore = false) {
    const at = openStack.indexOf(id)
    const wasOpen = at !== -1
    if (wasOpen) openStack.splice(at, 1)
    document.removeEventListener('keydown', onKeydown, true)
    const target = previouslyFocused
    previouslyFocused = null

    // Restore focus only when this was the LAST modal standing. A modal that
    // closes while another is still open sits underneath it, and focusing its
    // trigger would drag focus out of the top modal and behind it. The top
    // modal keeps focus and restores to its own trigger when it goes.
    //
    // Known limit: when a stack unwinds bottom-up in one tick, the top modal's
    // remembered trigger is an element of the modal below it, which is by then
    // gone or hidden — focus lands on <body> rather than the original opener.
    // Unwinding top-down (the normal order) restores correctly. Chaining the
    // remembered targets across instances is not worth it while nothing in the
    // app stacks these modals.
    if (!wasOpen || openStack.length > 0) return
    if (!target || typeof target.focus !== 'function') return

    const restore = () => {
      // Re-check at call time: a trigger torn down with the modal must not be
      // focused just before removal (a pointless focus/blur pair that scrolls
      // and is announced), and a modal opened in the meantime keeps focus.
      if (openStack.length > 0) return
      if (!document.contains(target)) return
      target.focus()
    }
    if (deferRestore) void nextTick(restore)
    else restore()
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
    // strand the listener or the focus. The restore is deferred one tick so it
    // runs AFTER Vue has detached this subtree: a trigger that went away with
    // the modal then fails the contains() check and is left alone, while a
    // trigger that outlives it (modal alone behind a v-if) still gets focus
    // back.
    deactivate(true)
  })

  return { dialogRef, focusInitial }
}
