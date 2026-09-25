import { computed, reactive, type ComputedRef } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'

/**
 * Spec 109-k (activity-scope-filters). Single source of truth for the URL
 * filter contract: specs/109-ux-navigation-consistency/contracts/url-filter-contract.md
 * ("Parameters" table, "Behaviour" rules, "Link map"). Every scope-aware page
 * (Activity, Usage, Tools, Servers, Review, Sessions, Clients, Tokens, ...)
 * reads and writes its filters through this composable, so the same
 * parameter name always means the same thing and maps to the same REST query
 * everywhere (parity test in 109-m).
 *
 * This is deliberately a general MECHANISM (registerScopeParam + a small
 * per-param toRest hook) rather than a hand-written switch over every page —
 * the contract table has ~20 rows, several with page-specific REST rules
 * (tool splitting, the session ws- prefix, the Usage `window` mapping), and a
 * mechanism is what lets Spec 108 add `profile`/`client`/`token`-shaped rows
 * later (T151) without touching this file.
 */

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

/** Every page whose filters flow through the contract (url-filter-contract.md). */
export type PageId =
  | 'activity'
  | 'usage'
  | 'tools'
  | 'servers'
  | 'review'
  | 'sessions'
  | 'clients'
  | 'tokens'
  | 'catalog'
  | 'add-server'
  | 'settings'
  | 'server-detail'
  | 'profile-editor'

/** Context passed to a ParamDef's custom toRest hook. */
export interface ScopeRestContext {
  page: PageId
  /** The full raw URL state for this page (every registered param, as strings). */
  state: Readonly<Record<string, string | undefined>>
  /** Resolves a relative or absolute time string to an absolute RFC3339 one. */
  resolveTime: (value: string) => string
}

export interface ScopeParamDef {
  /** URL parameter name. */
  name: string
  /**
   * REST query parameter name when it differs from `name`. Ignored when a
   * custom `toRest` hook is supplied (the hook owns the REST shape entirely).
   */
  rest?: string
  /** Carried across scope-aware pages by in-app links built with `linkTo`. */
  sticky?: boolean
  /** Pages that read/write this parameter from the URL at all. */
  pages: PageId[]
  /**
   * Pages on which this parameter is actually sent to REST (as opposed to
   * applied client-side only, or not applicable). Defaults to `pages`. An
   * empty array means "never sent to REST anywhere" (e.g. `q` on Tools).
   */
  restPages?: PageId[]
  /**
   * Availability feature name (e.g. "scope_filters"): the parameter is
   * hidden — no control, no chip, never sent to REST, but kept untouched in
   * the URL — until `setAvailableFeatures` lists this name (GET /api/v1/status
   * `features`, rule 7).
   */
  requires?: string
  /**
   * Custom REST mapping for this one parameter (tool splitting, the session
   * ws- prefix rule, the Usage `window` mapping, ...). Returning `undefined`
   * omits the parameter from `toRest()` entirely (e.g. a Usage range that
   * cannot be expressed as `window`). Returning `{}` also omits it.
   */
  toRest?: (value: string, ctx: ScopeRestContext) => Record<string, string> | undefined
  /** Human label for the chip (defaults to `name`). */
  label?: string
}

export interface ScopeChip {
  name: string
  label: string
  value: string
  remove: () => void
}

// ---------------------------------------------------------------------------
// Registry (module-level: one registration set for the whole app)
// ---------------------------------------------------------------------------

const registry = new Map<string, ScopeParamDef>()

/** Registers (or replaces) a scope parameter definition. */
export function registerScopeParam(def: ScopeParamDef): void {
  registry.set(def.name, def)
}

/** Test/debug helper: clears every registration. Never call from app code. */
export function _resetScopeParamRegistry(): void {
  registry.clear()
}

function defsForPage(page: PageId): ScopeParamDef[] {
  return Array.from(registry.values()).filter(d => d.pages.includes(page))
}

// ---------------------------------------------------------------------------
// Feature availability (GET /api/v1/status `features`, rule 7)
// ---------------------------------------------------------------------------

const availableFeatures = reactive<Set<string>>(new Set())

/** Called from the status poller/store once `features` is known. */
export function setAvailableFeatures(features: readonly string[] | undefined | null): void {
  availableFeatures.clear()
  for (const f of features ?? []) availableFeatures.add(f)
}

function isAvailable(def: ScopeParamDef): boolean {
  return !def.requires || availableFeatures.has(def.requires)
}

// ---------------------------------------------------------------------------
// Relative time resolution (from/to; url-filter-contract.md `from`, `to`)
// ---------------------------------------------------------------------------

const RELATIVE_TIME_RE = /^-([0-9]+)(m|h|d)$/

/**
 * Resolves a --from/--to-style value: an absolute RFC3339 timestamp passes
 * through unchanged, a relative shorthand ("-24h", "-7d", "-30m") resolves
 * against now. Anything else passes through unchanged (never throws — a page
 * decides for itself whether an unresolvable value is a disabled chip).
 */
export function resolveScopeTime(value: string, now: Date = new Date()): string {
  if (!value) return value
  const m = RELATIVE_TIME_RE.exec(value)
  if (!m) return value
  const amount = Number(m[1])
  const unit = m[2]
  const ms = unit === 'm' ? amount * 60_000 : unit === 'h' ? amount * 3_600_000 : amount * 86_400_000
  return new Date(now.getTime() - ms).toISOString().replace(/\.\d{3}Z$/, 'Z')
}

/** Maps a `from` (no `to`) value to a Usage `window` value, or undefined if
 * the range cannot be expressed as one of the three Usage presets
 * (url-filter-contract.md `from`/`to` row, "Usage: window"). */
export function usageWindowFor(from: string | undefined, to: string | undefined): string | undefined {
  if (to) return undefined // an explicit end time is never one of the three presets
  if (!from) return 'all'
  if (from === '-24h') return '24h'
  if (from === '-7d') return '7d'
  return undefined // e.g. -3d: not applied on Usage (disabled chip, rule 5)
}

/** Splits a `tool` URL value per the contract's --tool/`tool` rule: a
 * "server:tool" value becomes {server, tool}; a disagreeing explicit
 * `server` is kept as-is (never widened); a bare tool has no server. */
export function splitScopeTool(tool: string | undefined, explicitServer: string | undefined): { server?: string; tool?: string } {
  if (!tool) return { server: explicitServer || undefined }
  const idx = tool.indexOf(':')
  if (idx === -1) return { server: explicitServer || undefined, tool }
  const toolServer = tool.slice(0, idx)
  const toolName = tool.slice(idx + 1)
  return { server: explicitServer || toolServer, tool: toolName }
}

/** Routes a `session` URL value to the REST filter name per the existing CLI
 * rule (a `ws-` prefix is a work session id, anything else a raw MCP
 * transport session id). */
export function sessionRestParam(session: string): 'work_session_id' | 'session_id' {
  return session.startsWith('ws-') ? 'work_session_id' : 'session_id'
}

/** The Web-only "Other / internal" residual bucket on Activity `status`
 * (url-filter-contract.md `status` row): filtered client-side, never sent to
 * REST. */
export const OTHER_STATUS = 'other'

// ---------------------------------------------------------------------------
// Default parameter table (url-filter-contract.md "Parameters")
// ---------------------------------------------------------------------------

function registerDefaultScopeParams(): void {
  registerScopeParam({
    name: 'server',
    pages: ['tools', 'activity', 'usage', 'review'],
    restPages: ['activity', 'usage'], // client-side on Tools and Review
  })
  registerScopeParam({
    name: 'tool',
    pages: ['activity', 'usage'],
    toRest: (value, ctx) => {
      const { server, tool } = splitScopeTool(value, ctx.state.server)
      const out: Record<string, string> = {}
      if (server) out.server = server
      if (tool) out.tool = tool
      return out
    },
  })
  registerScopeParam({
    name: 'session',
    sticky: false,
    pages: ['activity'],
    toRest: value => ({ [sessionRestParam(value)]: value }),
  })
  registerScopeParam({
    name: 'status',
    pages: ['activity', 'usage', 'tools', 'servers'],
    restPages: ['activity', 'usage'], // client-side on Tools and Servers
    toRest: (value, ctx) => {
      if (ctx.page === 'activity' && value === OTHER_STATUS) return undefined
      return { status: value }
    },
  })
  registerScopeParam({
    name: 'from',
    sticky: true,
    pages: ['activity', 'usage'],
    // On Usage, `from`/`to` map to `window` — computed once from BOTH values
    // together (incl. when both are absent, `window=all`), which the
    // generic per-param loop cannot express (it only runs for a param that
    // has a value). See the dedicated Usage step in toRest() below.
    restPages: ['activity'],
    toRest: (value, ctx) => ({ start_time: ctx.resolveTime(value) }),
  })
  registerScopeParam({
    name: 'to',
    sticky: true,
    pages: ['activity', 'usage'],
    restPages: ['activity'],
    toRest: (value, ctx) => ({ end_time: ctx.resolveTime(value) }),
  })
  registerScopeParam({ name: 'view', pages: ['activity'], restPages: [] })
  registerScopeParam({ name: 'type', pages: ['activity'] })
  registerScopeParam({ name: 'tab', pages: ['server-detail', 'settings', 'clients', 'add-server'], restPages: [] })
  registerScopeParam({ name: 'q', pages: ['tools', 'servers', 'catalog'], restPages: ['catalog'] })
  registerScopeParam({ name: 'tier', pages: ['tools'], restPages: [], label: 'Tier' })
  registerScopeParam({ name: 'risk', pages: ['tools'], restPages: [] }) // alias of tier, resolved by the page
  registerScopeParam({ name: 'approval', pages: ['tools'], restPages: [] })
  registerScopeParam({ name: 'auth_type', pages: ['activity'] })
  registerScopeParam({ name: 'change', pages: ['review'], restPages: [] })
  registerScopeParam({ name: 'source', pages: ['add-server'] })
  registerScopeParam({ name: 'focus', pages: ['settings', 'server-detail', 'clients', 'profile-editor'], restPages: [], sticky: false })

  // Spec 108 rows, registered here under the ownership rule (109-k owns the
  // whole contract, incl. profile/client/token) and hidden until
  // features.scope_filters lists them.
  registerScopeParam({
    name: 'profile',
    sticky: true,
    requires: 'scope_filters',
    pages: ['activity', 'usage', 'tools', 'servers', 'clients', 'tokens'],
  })
  registerScopeParam({
    name: 'client',
    sticky: true,
    requires: 'scope_filters',
    pages: ['activity', 'usage', 'tools', 'clients'],
  })
  registerScopeParam({
    name: 'token',
    sticky: true,
    requires: 'scope_filters',
    pages: ['activity', 'usage', 'tokens'],
  })
}

registerDefaultScopeParams()

// ---------------------------------------------------------------------------
// Page → route mapping for linkTo
// ---------------------------------------------------------------------------

const pageRouteNames: Partial<Record<PageId, string>> = {
  activity: 'activity',
  usage: 'usage',
  tools: 'tools',
  servers: 'servers',
  review: 'review',
  sessions: 'sessions',
  tokens: 'tokens',
  // 'clients' has no route until Spec 109-h ships it; linkTo falls back to
  // a plain path so a caller that reaches for it early does not crash.
}

// ---------------------------------------------------------------------------
// useScopeQuery
// ---------------------------------------------------------------------------

export interface UseScopeQueryResult {
  state: Record<string, string | undefined>
  set: (patch: Record<string, string | undefined>) => void
  clear: (names?: string[]) => void
  toRest: () => Record<string, string>
  linkTo: (page: PageId, patch?: Record<string, string>) => RouteLocationRaw
  chips: ComputedRef<ScopeChip[]>
}

export function useScopeQuery(page: PageId): UseScopeQueryResult {
  const route = useRoute()
  const router = useRouter()

  function rawQueryValue(name: string): string | undefined {
    const v = route.query[name]
    if (Array.isArray(v)) return v[0] ?? undefined
    return v ?? undefined
  }

  // Rule 1: read on mount, before first fetch — the caller does this by
  // calling toRest()/state synchronously during setup, not in an onMounted
  // hook that races the initial fetch. `state` itself is always in sync with
  // route.query (Vue's reactivity + vue-router's reactive query object).
  const state = reactive<Record<string, string | undefined>>({})
  for (const def of defsForPage(page)) {
    Object.defineProperty(state, def.name, {
      enumerable: true,
      get: () => rawQueryValue(def.name),
    })
  }

  function set(patch: Record<string, string | undefined>): void {
    // zcode round 1 (F3): a valueless query param ("?foo", parsed by
    // vue-router as null) was dropped by every set()/clear() call, however
    // unrelated — unknown parameters must be "preserved untouched" (rule 6).
    const query: Record<string, string | string[] | null> = {}
    for (const [k, v] of Object.entries(route.query)) {
      if (v === undefined) continue
      query[k] = v as string | string[] | null
    }
    for (const [k, v] of Object.entries(patch)) {
      if (v === undefined || v === '') delete query[k]
      else query[k] = v
    }
    router.replace({ query })
  }

  function clear(names?: string[]): void {
    // zcode round 1 (F3): clearing "every contract parameter the page
    // supports" must still leave a hidden (unavailable) parameter untouched
    // in the URL (rule 7) — profile/client/token before features.scope_filters
    // lists them, e.g.
    const target = names ?? defsForPage(page).filter(isAvailable).map(d => d.name)
    const patch: Record<string, string | undefined> = {}
    for (const n of target) patch[n] = undefined
    set(patch)
  }

  function toRest(): Record<string, string> {
    const out: Record<string, string> = {}
    const snapshot: Record<string, string | undefined> = {}
    for (const def of defsForPage(page)) snapshot[def.name] = state[def.name]

    const ctx: ScopeRestContext = { page, state: snapshot, resolveTime: v => resolveScopeTime(v) }

    for (const def of defsForPage(page)) {
      const value = snapshot[def.name]
      if (value === undefined || value === '') continue
      if (!isAvailable(def)) continue // hidden: never sent to REST (rule 7)

      const restPages = def.restPages ?? def.pages
      if (!restPages.includes(page)) continue // client-side only / not applicable here

      if (def.toRest) {
        const mapped = def.toRest(value, ctx)
        if (mapped) Object.assign(out, mapped)
        continue
      }
      out[def.rest ?? def.name] = value
    }

    // Usage `window` (url-filter-contract.md `from`/`to` row): computed from
    // BOTH values together, including when neither is present (window=all) —
    // a case the generic per-param loop above cannot express, since it only
    // runs for a parameter that has a value.
    if (page === 'usage') {
      const w = usageWindowFor(snapshot.from, snapshot.to)
      if (w) out.window = w
    }

    return out
  }

  function linkTo(target: PageId, patch: Record<string, string> = {}): RouteLocationRaw {
    const query: Record<string, string> = {}
    // Rule 3: sticky params carry (profile/client/token only once available).
    for (const def of registry.values()) {
      if (!def.sticky) continue
      if (!def.pages.includes(target)) continue
      if (!isAvailable(def)) continue
      const v = rawQueryValue(def.name)
      if (v) query[def.name] = v
    }
    Object.assign(query, patch)
    const name = pageRouteNames[target]
    return name ? { name, query } : { path: `/${target}`, query }
  }

  const chips = computed<ScopeChip[]>(() => {
    const out: ScopeChip[] = []
    for (const def of defsForPage(page)) {
      if (!isAvailable(def)) continue
      const value = state[def.name]
      if (value === undefined || value === '') continue
      out.push({
        name: def.name,
        label: def.label ?? def.name,
        value,
        remove: () => clear([def.name]),
      })
    }
    return out
  })

  return { state, set, clear, toRest, linkTo, chips }
}
