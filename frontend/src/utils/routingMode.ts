/**
 * Routing-mode and serialization-mode vocabulary for the header mode switcher.
 *
 * `Mode: Retrieve` sat in the header with nothing to explain it (audit F31) —
 * it is the single most consequential setting on the page (it decides what an
 * agent sees when it connects) and it was rendered as a bare word, with a
 * `cursor-help` that produced a question mark and no hint. Label, explanation
 * and trade-off live together here so the badge, the switcher and any future
 * surface cannot drift apart.
 *
 * Two DIFFERENT axes are described in this file and must not be conflated:
 *
 *  - `routing_mode` picks the tool SURFACE served on /mcp (retrieve / direct /
 *    code execution). It binds to an http.ServeMux pattern at startup, so
 *    changing it requires a core restart.
 *  - `tool_response_mode` (Spec 085) and `direct_tool_response_mode` (Spec 102)
 *    pick how each entry on a surface is SERIALIZED. Both hot-reload.
 */

export interface RoutingModeMeta {
  /** Config value. */
  mode: string
  /** Compact label rendered in the header badge. */
  label: string
  /** One-sentence explanation, shown as the badge's tooltip. */
  description: string
  /** What choosing this mode costs or buys — the informed-decision half. */
  tradeoff: string
  /** Dedicated endpoint that always serves this mode, restart or not. */
  endpoint: string
}

const RETRIEVE: RoutingModeMeta = {
  mode: 'retrieve_tools',
  label: 'Retrieve',
  description:
    'Retrieve mode: agents search for the tools they need with retrieve_tools, then call them — only matching tools enter the agent’s context.',
  tradeoff:
    'Smallest context: an agent sees a handful of meta-tools instead of your whole catalog, but has to search before it can call anything.',
  endpoint: '/mcp/call',
}

const DIRECT: RoutingModeMeta = {
  mode: 'direct',
  label: 'Direct',
  description:
    'Direct mode: every enabled tool from every server is listed to the agent up front — no search step, but the full tool list costs context.',
  tradeoff:
    'Nothing is hidden and there is no search step, but the whole catalog sits in the prompt from the first message — roughly 190 tokens per tool with schemas.',
  endpoint: '/mcp/all',
}

const CODE_EXECUTION: RoutingModeMeta = {
  mode: 'code_execution',
  label: 'Code Exec',
  description:
    'Code execution mode: agents orchestrate several upstream tools from one sandboxed JavaScript call, discovering them with retrieve_tools.',
  tradeoff:
    'Fewest round trips for multi-step work — one sandboxed JavaScript call can chain several tools. Needs code execution enabled in Settings.',
  endpoint: '/mcp/code',
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

/**
 * Why a routing-mode switch cannot take effect until the core restarts, and the
 * way around it. /mcp is bound to ONE mcp-go server instance at startup
 * (internal/server/server.go → GetMCPServerForMode) on an http.ServeMux, which
 * cannot re-register a pattern; the dedicated routes are each permanently bound
 * to their own mode by design (Spec 031) and are unaffected.
 */
export const ROUTING_RESTART_NOTE =
  'Changing the surface on /mcp needs a restart — /mcp binds its mode when mcpproxy starts. Nothing to restart if your client can point at a dedicated endpoint instead: each one always serves its own mode.'

export interface SerializationModeMeta {
  /** Config value. */
  value: string
  /** Label for the option row. */
  label: string
  /** What the agent actually receives, and what it costs. */
  description: string
}

/**
 * `tool_response_mode` (Spec 085) — how each retrieve_tools search RESULT is
 * rendered. Never changes which tools are found.
 */
export const TOOL_RESPONSE_MODES: SerializationModeMeta[] = [
  {
    value: 'full',
    label: 'Full schemas',
    description:
      'Every search result carries its complete input schema — nothing extra for the agent to fetch.',
  },
  {
    value: 'compact',
    label: 'Signatures, schema on demand',
    description:
      'A one-line signature plus a first-sentence description; the agent pulls a full schema with describe_tool when it needs one. Same tools are found either way.',
  },
]

/**
 * `direct_tool_response_mode` (Spec 102) — how each entry of a direct-surface
 * tools/list is rendered. Same tools, same names, same annotations either way.
 * The savings quoted here are the measured ones from the shipped feature, not
 * the original projection: SC-001 was restated because names and descriptions,
 * not schemas, dominate these corpora.
 */
export const DIRECT_TOOL_RESPONSE_MODES: SerializationModeMeta[] = [
  {
    value: 'full',
    label: 'Full schemas',
    description:
      'Every tool is listed with its complete input schema. Keep this for clients that build forms from the advertised schema.',
  },
  {
    value: 'deferred',
    label: 'Signatures, schema on demand',
    description:
      'Same tools and names without the schemas: measured 29.7% smaller on a 45-tool listing, 34.8% on 527 tools. Tools marked ~ cost one describe_tool call; a wrong guess is rejected before it reaches the server, with the schema attached.',
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
  return routingMode === 'retrieve_tools' || !routingMode ? '/mcp and /mcp/call' : '/mcp/call'
}

/** Why the retrieve axis does not reach /mcp under code execution. */
export const TOOL_RESPONSE_CODE_EXEC_NOTE =
  'Code-execution mode always sends full schemas on /mcp — describe_tool is not exposed there, so there is nothing to fetch a deferred schema with. This setting still governs /mcp/call.'

export function directToolResponseSurface(routingMode: string | undefined | null): string {
  return routingMode === 'direct' ? '/mcp and /mcp/all' : '/mcp/all'
}
