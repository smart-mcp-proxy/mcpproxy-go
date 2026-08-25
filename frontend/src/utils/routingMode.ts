/**
 * Routing-mode vocabulary for the header badge.
 *
 * `Mode: Retrieve` sat in the header with nothing to explain it (audit F31) —
 * it is the single most consequential setting on the page (it decides what an
 * agent sees when it connects) and it was rendered as a bare word. Label and
 * explanation live together here so the badge, its tooltip and any future
 * surface cannot drift apart.
 */

export interface RoutingModeMeta {
  /** Compact label rendered in the header badge. */
  label: string
  /** One-sentence explanation, shown as the badge's tooltip. */
  description: string
}

const RETRIEVE: RoutingModeMeta = {
  label: 'Retrieve',
  description:
    'Retrieve mode: agents search for the tools they need with retrieve_tools, then call them — only matching tools enter the agent’s context.',
}

const ROUTING_MODES: Record<string, RoutingModeMeta> = {
  direct: {
    label: 'Direct',
    description:
      'Direct mode: every enabled tool from every server is listed to the agent up front — no search step, but the full tool list costs context.',
  },
  code_execution: {
    label: 'Code Exec',
    description:
      'Code execution mode: agents orchestrate several upstream tools from one sandboxed JavaScript call, discovering them with retrieve_tools.',
  },
  retrieve_tools: RETRIEVE,
}

/** Metadata for a routing mode, falling back to Retrieve — the default. */
export function routingModeMeta(mode: string | undefined | null): RoutingModeMeta {
  if (!mode) return RETRIEVE
  return ROUTING_MODES[mode] ?? RETRIEVE
}
