import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// Spec 109 FR-055 / T007 / T019: one z-index scale, defined in one place
// (navigation-map.md: header < sidebar < dropdown < modal < toast — the
// sidebar must outrank the header so the mobile `drawer-side` overlay is
// never painted over by the sticky header when it slides open; review
// round 1 caught the scale shipping with sidebar and header swapped), and no
// `<dialog class="modal">` still relies on the `:open` attribute binding (or
// a `modal-open` class toggle) — every one of them now drives its open state
// through `useDialogOpen` (`showModal()`/`close()`, top layer), which is what
// keeps a modal from ever being painted over by the sidebar/header (H4). The
// real cross-browser "does the modal render above the sidebar" check is a
// Playwright step (e2e/web-ui-sweep/visual-a11y-sweep.spec.ts) since jsdom
// does not implement layout or `<dialog>` natively.

function resolve(relative: string): string {
  return fileURLToPath(new URL(relative, import.meta.url))
}

function readSrc(relative: string): string {
  return readFileSync(resolve(relative), 'utf-8')
}

describe('z-index scale (frontend/src/assets/z-index.css)', () => {
  const css = readSrc('../../src/assets/z-index.css')

  function tokenValue(name: string): number {
    const match = css.match(new RegExp(`--z-${name}:\\s*(\\d+)`))
    if (!match) throw new Error(`token --z-${name} not found in z-index.css`)
    return Number(match[1])
  }

  it('defines the full navigation-map scale', () => {
    for (const name of ['sidebar', 'header', 'dropdown', 'modal', 'toast']) {
      expect(css).toMatch(new RegExp(`--z-${name}:\\s*\\d+`))
    }
  })

  it('orders header < sidebar < dropdown < modal < toast', () => {
    const sidebar = tokenValue('sidebar')
    const header = tokenValue('header')
    const dropdown = tokenValue('dropdown')
    const modal = tokenValue('modal')
    const toast = tokenValue('toast')
    // Sidebar must outrank header: `.drawer-side` (SidebarNav) is a fixed
    // overlay sibling of the sticky TopHeader, and on <lg (the drawer
    // breakpoint) opening it must paint over the header, not sit under it
    // (review round 1 — the PR shipped this inverted).
    expect(header).toBeLessThan(sidebar)
    expect(sidebar).toBeLessThan(dropdown)
    expect(dropdown).toBeLessThan(modal)
    expect(modal).toBeLessThan(toast)
  })

  it('is imported by the app entrypoint', () => {
    const main = readSrc('../../src/main.ts')
    expect(main).toContain('./assets/z-index.css')
  })
})

describe('SidebarNav no longer hardcodes a z-40 that can outrank a modal', () => {
  it('uses the --z-sidebar token, not a bare z-40, on drawer-side', () => {
    const src = readSrc('../../src/components/SidebarNav.vue')
    expect(src).not.toMatch(/class="drawer-side z-40"/)
    expect(src).toContain('--z-sidebar')
  })
})

describe('Repositories.vue dialogs use showModal()/close(), not :open (FR-055)', () => {
  const src = readSrc('../../src/views/Repositories.vue')

  it('has no :open="..." binding on any <dialog>', () => {
    expect(src).not.toMatch(/<dialog[^>]*:open=/)
  })

  it('names all three dialogs and drives them via ref + useDialogOpen', () => {
    for (const testId of [
      'registry-required-input-dialog',
      'registry-add-source-dialog',
      'registry-delete-dialog',
    ]) {
      expect(src).toContain(testId)
    }
    expect(src).toContain('useDialogOpen')
  })
})

describe('every <dialog class="modal"> drives its open state imperatively', () => {
  const files = [
    '../../src/components/OnboardingWizard.vue',
    '../../src/components/ConnectModal.vue',
    '../../src/components/AddSecretModal.vue',
    '../../src/components/AddServerModal.vue',
    '../../src/views/Repositories.vue',
    '../../src/views/teams/UserTokens.vue',
    '../../src/views/teams/UserActivity.vue',
    '../../src/views/teams/UserServers.vue',
  ]

  it.each(files)('%s has no <dialog :open=...> or <dialog ... modal-open> toggle', (relative) => {
    const src = readSrc(relative)
    // Matches the opening tag only (non-greedy up to the first '>'), so a
    // nested plain <div class="modal modal-open"> confirm box rendered
    // *inside* an already-promoted <dialog> (not itself a stacking risk) does
    // not false-positive this check.
    expect(src).not.toMatch(/<dialog\b[^>]*?:open=/)
    expect(src).not.toMatch(/<dialog\b[^>]*?modal-open/)
  })
})
