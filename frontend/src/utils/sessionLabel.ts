/**
 * Human-meaningful labels for MCP sessions.
 *
 * A session is one AI client's conversation with the proxy. The raw session id
 * is an opaque uuid, so surfacing it (`...139c9`) tells the user nothing. The
 * MCP `initialize` handshake already gives us `clientInfo.name`, which the core
 * persists on the session record and serves on `GET /api/v1/sessions` — that is
 * the name we show.
 *
 * Display names mirror the macOS tray (DashboardView.swift `connectedClientNames`)
 * so the two surfaces agree on what a client is called.
 */

/** Known clients, matched case-insensitively as substrings of clientInfo.name. */
const KNOWN_CLIENTS: Array<[needle: string, display: string]> = [
  ['claude', 'Claude Code'],
  ['cursor', 'Cursor'],
  ['vscode', 'VS Code'],
  ['copilot', 'VS Code'],
  ['codex', 'Codex'],
  ['gemini', 'Gemini'],
  ['antigravity', 'Antigravity'],
  ['windsurf', 'Windsurf'],
]

/**
 * Map a raw MCP clientInfo.name to a display name. Unknown clients are shown
 * verbatim — a name we don't recognise is still far better than a hash.
 */
export function prettyClientName(raw?: string): string {
  const name = (raw ?? '').trim()
  if (!name) return ''
  const lower = name.toLowerCase()
  for (const [needle, display] of KNOWN_CLIENTS) {
    if (lower.includes(needle)) return display
  }
  return name
}

/** Last 5 chars of a session id — the legacy label, now only ever a fallback. */
export function sessionIdSuffix(sessionId: string): string {
  return sessionId.slice(-5)
}

function clockTime(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

export interface SessionLabelInput {
  sessionId: string
  /** Raw clientInfo.name, e.g. "claude-code". Absent for sessions we never saw initialize. */
  clientName?: string
  /** ISO start time, used to tell two sessions of the same client apart. */
  startTime?: string
  /**
   * True when another session in the same list would render an identical label.
   * Callers compute this across the whole list; we then disambiguate with the id.
   */
  ambiguous?: boolean
}

/**
 * Build the label shown in the Activity Log session filter.
 *
 *   "Claude Code · 14:32"          — the common case
 *   "Claude Code · 14:32 (139c9)"  — two Claude Code sessions started the same minute
 *   "...139c9"                     — no clientInfo (pre-initialize or a stale session)
 */
export function formatSessionLabel(input: SessionLabelInput): string {
  const pretty = prettyClientName(input.clientName)
  if (!pretty) {
    // Unchanged legacy behaviour: without a client name there is nothing better to show.
    return `...${sessionIdSuffix(input.sessionId)}`
  }

  const time = clockTime(input.startTime)
  const base = time ? `${pretty} · ${time}` : pretty
  return input.ambiguous ? `${base} (${sessionIdSuffix(input.sessionId)})` : base
}

/**
 * Label a whole list at once, resolving collisions so no two sessions share a
 * label. Returns a Map keyed by session id.
 */
export function buildSessionLabels(
  sessions: Array<Omit<SessionLabelInput, 'ambiguous'>>
): Map<string, string> {
  const counts = new Map<string, number>()
  for (const s of sessions) {
    const provisional = formatSessionLabel(s)
    counts.set(provisional, (counts.get(provisional) ?? 0) + 1)
  }

  const labels = new Map<string, string>()
  for (const s of sessions) {
    const provisional = formatSessionLabel(s)
    const ambiguous = (counts.get(provisional) ?? 0) > 1
    labels.set(s.sessionId, formatSessionLabel({ ...s, ambiguous }))
  }
  return labels
}
