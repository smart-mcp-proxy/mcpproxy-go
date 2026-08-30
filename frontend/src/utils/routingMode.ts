/**
 * Routing-mode and serialization-mode vocabulary for the header mode switcher.
 *
 * `Mode: Retrieve` sat in the header with nothing to explain it (audit F31) —
 * the single most consequential setting on the page, rendered as a bare word
 * with a `cursor-help` that produced a question mark and no hint.
 *
 * Each entry carries THREE lengths of the same fact, because a dropdown that
 * must not scroll cannot afford one long one:
 *   - `label`   — the chip.
 *   - `summary` — one short line, always visible. What it does, in ~8 words.
 *   - `detail`  — the full explanation and its trade-off, shown on hover
 *                 (`title`) and linked to the docs. Never rendered inline.
 *
 * Two DIFFERENT axes are described here and must not be conflated:
 *
 *  - `routing_mode` picks the tool SURFACE served on /mcp (retrieve / direct /
 *    code execution). It binds to an http.ServeMux pattern at startup, so
 *    changing it requires a core restart.
 *  - `tool_response_mode` (Spec 085) and `direct_tool_response_mode` (Spec 102)
 *    pick how each entry on a surface is SERIALIZED. Both hot-reload.
 */

/** Published docs, not repo paths — these are rendered as links. */
export const ROUTING_MODES_DOC = 'https://docs.mcpproxy.app/features/routing-modes'
/** The retrieve axis (Spec 085) — a different page from the direct one. */
export const TOOL_RESPONSE_DOC = 'https://docs.mcpproxy.app/features/search-discovery'
/** The direct axis (Spec 102). */
export const DIRECT_TOOL_RESPONSE_DOC =
  'https://docs.mcpproxy.app/features/schema-deferred-direct-mode'

export interface RoutingModeMeta {
  /** Config value. */
  mode: string
  /** Compact label rendered in the header badge and the option row. */
  label: string
  /** One short line, always visible. */
  summary: string
  /** Full explanation + trade-off. Hover hint and tooltip only. */
  detail: string
  /** Dedicated endpoint that always serves this mode, restart or not. */
  endpoint: string
  /**
   * Set when the mode needs something else switched on first. Rendered INLINE
   * when unmet — a prerequisite that only appears on hover is one the operator
   * discovers after restarting into a surface that cannot call anything.
   */
  prerequisiteNote?: string
}

const RETRIEVE: RoutingModeMeta = {
  mode: 'retrieve_tools',
  label: 'Retrieve',
  summary: 'Search first — a few meta-tools.',
  detail:
    'Agents search with retrieve_tools, then call what they found, so only matching tools enter the context. An agent sees a handful of meta-tools instead of your whole catalog, but has to search before it can call anything.',
  endpoint: '/mcp/call',
}

const DIRECT: RoutingModeMeta = {
  mode: 'direct',
  label: 'Direct',
  summary: 'All tools up front — ~190 tokens each.',
  detail:
    'Every tool visible to the session is listed to the agent on connect — quarantined, disabled and out-of-profile servers are still filtered out. No search step, but the whole catalog sits in the prompt from the first message, roughly 190 tokens per tool with full schemas.',
  endpoint: '/mcp/all',
}

const CODE_EXECUTION: RoutingModeMeta = {
  mode: 'code_execution',
  label: 'Code Exec',
  summary: 'Search first, then chain tools in JS.',
  detail:
    'Agents discover tools with retrieve_tools and orchestrate several of them from one sandboxed JavaScript call — the fewest round trips for multi-step work. Requires code execution to be enabled in Settings; without it this surface can find tools but call none of them.',
  endpoint: '/mcp/code',
  prerequisiteNote: 'Enable code execution in Settings first.',
}

const ROUTING_MODES: Record<string, RoutingModeMeta> = {
  direct: DIRECT,
  code_execution: CODE_EXECUTION,
  retrieve_tools: RETRIEVE,
}

/** The three modes, in the order the switcher lists them. */
export const ROUTING_MODE_LIST: RoutingModeMeta[] = [RETRIEVE, DIRECT, CODE_EXECUTION]

/** Metadata for a routing mode, falling back to Retrieve — the default. */
export function routingModeMeta(mode: string | undefined | null): RoutingModeMeta {
  if (!mode) return RETRIEVE
  return ROUTING_MODES[mode] ?? RETRIEVE
}

/** One line under the surface list. The reason lives in the hover hint. */
export const ROUTING_RESTART_NOTE = 'Changing /mcp needs a restart — its endpoint never does.'

/**
 * Why. /mcp is bound to ONE mcp-go server instance at startup
 * (internal/server/server.go → GetMCPServerForMode) on an http.ServeMux, which
 * cannot re-register a pattern; the dedicated routes are each permanently bound
 * to their own mode by design (Spec 031) and are unaffected.
 */
export const ROUTING_RESTART_HINT =
  '/mcp binds its mode when mcpproxy starts, so switching it takes effect on the next start. Nothing to restart if your client can point at a dedicated endpoint instead: each one always serves its own mode.'

export interface SerializationModeMeta {
  /** Config value. */
  value: string
  /** Label for the option row. */
  label: string
  /** One short line, always visible. */
  summary: string
  /** What the agent actually receives, and what it costs. Hover hint only. */
  detail: string
}

/**
 * `tool_response_mode` (Spec 085) — how each retrieve_tools search RESULT is
 * rendered. Never changes which tools are found.
 */
export const TOOL_RESPONSE_MODES: SerializationModeMeta[] = [
  {
    value: 'full',
    label: 'Full schemas',
    summary: 'Complete input schema per result.',
    detail:
      'Every search result carries its complete input schema — nothing extra for the agent to fetch.',
  },
  {
    value: 'compact',
    label: 'Signatures',
    summary: 'Signature now, schema on demand.',
    detail:
      'A one-line signature plus a first-sentence description; the agent pulls a full schema with describe_tool when it needs one. Saves tokens, and never changes which tools are found.',
  },
]

/**
 * `direct_tool_response_mode` (Spec 102) — how each entry of a direct-surface
 * tools/list is rendered. Same tools, same names, same annotations either way.
 * The savings quoted are the measured ones from the shipped feature, not the
 * original projection: SC-001 was restated because names and descriptions, not
 * schemas, dominate these corpora.
 */
export const DIRECT_TOOL_RESPONSE_MODES: SerializationModeMeta[] = [
  {
    value: 'full',
    label: 'Full schemas',
    summary: 'Complete input schema per tool.',
    detail:
      'Every tool is listed with its complete input schema. Keep this for clients that build forms from the advertised schema.',
  },
  {
    value: 'deferred',
    label: 'Signatures',
    summary: '~30% smaller listing, schema on demand.',
    detail:
      'Same tools and names without the schemas: measured 29.7% smaller on a 45-tool listing and 34.8% on 527 tools. Tools marked ~ cost one describe_tool call; a wrong guess is rejected before it reaches the server, with the schema attached.',
  },
]

/** Metadata for a serialization value, falling back to full — the default. */
export function serializationModeMeta(
  axis: SerializationModeMeta[],
  value: string | undefined | null
): SerializationModeMeta {
  return axis.find((m) => m.value === value) ?? axis[0]
}

/**
 * Which surface each serialization axis governs right now. Both axes are always
 * in effect SOMEWHERE — the dedicated endpoints never go away — so the switcher
 * names the endpoint rather than greying an axis out as inactive.
 *
 * Code-execution mode is deliberately NOT counted for the retrieve axis: that
 * surface pins itself to full schemas because describe_tool is not exposed on
 * it (Spec 085 FR-011), so a compact setting would point the agent at a tool it
 * cannot call. Claiming /mcp there would be a lie the operator could act on.
 */
export function toolResponseSurface(routingMode: string | undefined | null): string {
  return routingMode === 'retrieve_tools' || !routingMode ? '/mcp · /mcp/call' : '/mcp/call'
}

export function directToolResponseSurface(routingMode: string | undefined | null): string {
  return routingMode === 'direct' ? '/mcp · /mcp/all' : '/mcp/all'
}

/** Why the retrieve axis does not reach /mcp under code execution. */
export const TOOL_RESPONSE_CODE_EXEC_NOTE = 'Code Exec always sends full schemas on /mcp.'

export const TOOL_RESPONSE_CODE_EXEC_HINT =
  'describe_tool is not exposed on the code-execution surface, so there would be nothing to fetch a deferred schema with. This setting still governs /mcp/call.'
