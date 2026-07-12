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
 * Max rendered length of an unrecognised client name.
 *
 * `clientInfo.name` is attacker-controllable: any MCP client can send an
 * arbitrary string at initialize and the core stores it verbatim. Vue escapes it
 * on render, so there is no XSS — but an unbounded name would still be dumped
 * into a `<select>` option and blow out the filter layout. Bound it.
 */
const MAX_CLIENT_NAME = 32

/** Shortest id suffix we ever show. Grows on demand to stay unique. */
const MIN_SUFFIX = 5

/**
 * Map a raw MCP clientInfo.name to a display name. Unknown clients are shown
 * verbatim but length-bounded — a name we don't recognise is still far better
 * than a hash.
 */
export function prettyClientName(raw?: string): string {
  const name = (raw ?? '').trim()
  if (!name) return ''

  const lower = name.toLowerCase()
  for (const [needle, display] of KNOWN_CLIENTS) {
    if (lower.includes(needle)) return display
  }

  // Unrecognised, and therefore untrusted: collapse whitespace (a newline would
  // break the option label) and truncate.
  const flat = name.replace(/\s+/g, ' ')
  return flat.length > MAX_CLIENT_NAME ? `${flat.slice(0, MAX_CLIENT_NAME - 1)}…` : flat
}

/** Last `len` chars of a session id. */
export function sessionIdSuffix(sessionId: string, len: number = MIN_SUFFIX): string {
  return sessionId.slice(-len)
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
   * The project the client is working in (Spec 082) — the basename of its
   * workspace root, e.g. "mcpproxy-go". Absent for clients that do not disclose
   * roots (measured: Codex does not), in which case we fall back to the time.
   */
  workspace?: string
}

/** The label with no disambiguating suffix. "" when there is no client name. */
function baseLabel(input: SessionLabelInput): string {
  const pretty = prettyClientName(input.clientName)
  if (!pretty) return '' // no client name → the id is all we have

  // The project is the most useful thing we can say: it is what the user was
  // actually working on ("Claude Code · mcpproxy-go"), which is the question the
  // session filter exists to answer. Time is the fallback for clients that do
  // not disclose a workspace (measured: Codex does not).
  const workspace = (input.workspace ?? '').trim()
  if (workspace) return `${pretty} · ${workspace}`

  const time = clockTime(input.startTime)
  return time ? `${pretty} · ${time}` : pretty
}

/**
 * Build the label shown in the Activity Log session filter.
 *
 *   "Claude Code · 14:32"          — the common case
 *   "Claude Code · 14:32 (139c9)"  — two Claude Code sessions started the same minute
 *   "...139c9"                     — no clientInfo (pre-initialize, or an evicted session)
 *
 * `suffixLen` is chosen by buildSessionLabels so the suffix is long enough to
 * actually disambiguate — a fixed-width suffix can itself collide.
 */
export function formatSessionLabel(
  input: SessionLabelInput,
  opts: { ambiguous?: boolean; suffixLen?: number } = {}
): string {
  const len = opts.suffixLen ?? MIN_SUFFIX
  const base = baseLabel(input)

  // No client name: the id suffix IS the label (unchanged legacy behaviour).
  if (!base) return `...${sessionIdSuffix(input.sessionId, len)}`

  return opts.ambiguous ? `${base} (${sessionIdSuffix(input.sessionId, len)})` : base
}

/**
 * Shortest suffix length that tells every id in `ids` apart.
 *
 * A fixed 5-char suffix is NOT guaranteed unique — two ids can share their last
 * five characters — so grow it until the ids are distinguished, falling back to
 * the longest id. Session ids are unique, so this always terminates.
 */
function uniqueSuffixLen(ids: string[]): number {
  const maxLen = Math.max(...ids.map(id => id.length))
  for (let len = MIN_SUFFIX; len < maxLen; len++) {
    if (new Set(ids.map(id => id.slice(-len))).size === ids.length) return len
  }
  return maxLen
}

/**
 * Label a whole list at once, resolving collisions so no two sessions ever share
 * a label. Returns a Map keyed by session id.
 */
export function buildSessionLabels(sessions: SessionLabelInput[]): Map<string, string> {
  // Group by the label they would get with no disambiguation. Sessions with no
  // client name all share the "" group: their label is the id suffix, which must
  // be unique among themselves too.
  const groups = new Map<string, SessionLabelInput[]>()
  for (const s of sessions) {
    const key = baseLabel(s)
    const group = groups.get(key)
    if (group) group.push(s)
    else groups.set(key, [s])
  }

  const labels = new Map<string, string>()
  for (const [key, group] of groups) {
    const named = key !== ''

    // A named session alone in its group needs no suffix at all.
    if (named && group.length === 1) {
      labels.set(group[0].sessionId, formatSessionLabel(group[0]))
      continue
    }

    // Otherwise every member gets a suffix, long enough to be unique within the
    // group. (Unnamed sessions always carry one — it is their whole label.)
    const suffixLen = group.length > 1 ? uniqueSuffixLen(group.map(s => s.sessionId)) : MIN_SUFFIX
    for (const s of group) {
      labels.set(s.sessionId, formatSessionLabel(s, { ambiguous: named, suffixLen }))
    }
  }
  return labels
}
