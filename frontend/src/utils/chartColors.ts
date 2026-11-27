import { onBeforeUnmount, onMounted, ref, type Ref } from 'vue'

// Spec 134 FR-062: chart colours come from the design tokens, never from literals,
// so they follow the theme and keep the contrast the tokens are tested for.
//   brand  --brand-text   bars and the "calls" series (42% L in light, 78% L in dark)
//   ok     --status-ok    an error rate of zero
//   error  --status-error a non-zero error rate, the "errors" series
//   muted  --tone-muted   axis text and legend labels
//   grid   --ds-border    grid lines
export interface ChartColors { brand: string; ok: string; error: string; muted: string; grid: string }

// Last-resort values (corporate theme) for an environment with no computed styles.
export const FALLBACK_CHART_COLORS: ChartColors = {
  brand: 'oklch(42% 0.158 241.966)',
  ok: 'oklch(39% 0.194 149.214)',
  error: 'oklch(46% 0.191 22.216)',
  muted: 'oklch(46% 0.031 278.072)',
  grid: 'oklch(86% 0 0)',
}

const SOURCES: Record<keyof ChartColors, string> = {
  brand: '--brand-text',
  ok: '--status-ok',
  error: '--status-error',
  muted: '--tone-muted',
  grid: '--ds-border',
}

/** Resolve a token to a concrete colour string (`color-mix` and `var()` resolved by the browser). */
function resolveToken(name: string, fallback: string): string {
  if (typeof document === 'undefined' || typeof getComputedStyle === 'undefined') return fallback
  const probe = document.createElement('span')
  probe.style.color = `var(${name})`
  probe.style.display = 'none'
  document.body.appendChild(probe)
  const value = getComputedStyle(probe).color
  probe.remove()
  return value && value !== '' ? value : fallback
}

export function readChartColors(): ChartColors {
  const out = { ...FALLBACK_CHART_COLORS }
  for (const key of Object.keys(SOURCES) as (keyof ChartColors)[]) out[key] = resolveToken(SOURCES[key], FALLBACK_CHART_COLORS[key])
  return out
}

/** Chart colours that re-read when the theme (`<html data-theme>`) changes. */
export function useChartColors(): Ref<ChartColors> {
  const colors = ref<ChartColors>(readChartColors())
  let observer: MutationObserver | null = null
  onMounted(() => {
    if (typeof MutationObserver === 'undefined') return
    observer = new MutationObserver(() => {
      const next = readChartColors()
      if (JSON.stringify(next) !== JSON.stringify(colors.value)) colors.value = next
    })
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  })
  onBeforeUnmount(() => observer?.disconnect())
  return colors
}

/** Axis, legend and grid options shared by the four charts. */
export function chartTextOptions(c: ChartColors) {
  return { ticks: { color: c.muted }, grid: { color: c.grid }, border: { color: c.grid } }
}
