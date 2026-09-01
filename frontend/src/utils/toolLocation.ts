// Parsing `ScanFinding.location` back into the tool it names.
//
// The baseline (in-process) scanner builds the location as
// `fmt.Sprintf("%s:%s", tool.Server, tool.Name)` — byte-identical to
// `storage.ToolApprovalKey` (internal/security/detect/aggregate.go). Server names
// legitimately contain '.' and '/', e.g.
//
//     com.googleapis.sqladmin/mcp:create_user
//
// so the split is on the LAST colon, never the first.
//
// The parse is also ANCHORED to the server the caller already knows about, and
// refuses to guess without one. `location` is a free-form string shared with
// scanners that mean something entirely different by it
// (internal/security/scanner/engine.go, sarif.go):
//
//     "tool:" + result.ToolName              (cisco-mcp-scanner, :1014)
//     yr.TargetType + ":" + yr.TargetName    (yara/ramparts, :1207)
//     issueType + ":" + subject              (ramparts tool|prompt|resource, :1263)
//     "target/dist/tools/get-env.js"         (file-path locations, engine_test.go)
//     "src/index.js:42"                      (SARIF URI + ":" + startLine, sarif.go)
//
// Guessing a server/tool pair out of those produces a link to a server that does
// not exist. Every one of them must come back `null` so the caller falls back to
// today's inert <code>.

import type { SecurityScanFinding } from '@/types/api'
import { serverDetailPath } from '@/utils/serverRoute'

export interface ToolLocation {
  server: string
  tool: string
}

// MCP tool names come from the MCP tool surface and are identifier-shaped. This
// is what rejects "ramparts prompt:<free text subject>" and Windows paths like
// "C:\\Users\\x\\tool.js", which would otherwise last-colon-split into garbage.
const TOOL_NAME_RE = /^[A-Za-z0-9_.-]+$/

export interface ParseToolLocationOptions {
  /**
   * The server(s) the location may name. REQUIRED in practice: the parsed server
   * must equal one of these exactly, and a location that matches none of them
   * comes back null.
   *
   * There is deliberately no heuristic fallback for the case where the caller
   * does not know the server. Without an anchor, `src/index.js:42` — the shape
   * SARIF findings use (`URI + ":" + startLine`, scanner/sarif.go) — parses just
   * as happily as `github:create_issue`, and the caller renders a confident blue
   * link to a server that does not exist. A guess is not better than an inert
   * `<code>`, which is exactly what today's UI already shows.
   */
  expectedServer?: string | string[] | null
}

/** Parse `server:tool` out of a finding location, or null when it is not one. */
export function parseToolLocation(
  location: string | null | undefined,
  options: ParseToolLocationOptions = {},
): ToolLocation | null {
  const raw = (location ?? '').trim()
  if (!raw) return null

  const idx = raw.lastIndexOf(':')
  // idx <= 0 covers "no colon at all" (file paths) and a leading colon.
  if (idx <= 0 || idx === raw.length - 1) return null

  const server = raw.slice(0, idx)
  const tool = raw.slice(idx + 1)
  if (!TOOL_NAME_RE.test(tool)) return null
  // A server name never contains whitespace; a prose subject frequently does.
  if (/\s/.test(server)) return null

  const expected = normalizeExpected(options.expectedServer)
  if (expected.length === 0) return null
  return expected.includes(server) ? { server, tool } : null
}

function normalizeExpected(expected: string | string[] | null | undefined): string[] {
  if (!expected) return []
  const list = Array.isArray(expected) ? expected : [expected]
  return list.map((name) => name?.trim()).filter((name): name is string => !!name)
}

/**
 * Route into the server's Tools tab, deep-linked to one tool card. `?tool=` is
 * what ServerDetail reads to scroll the card into view and focus it.
 */
export function toolFocusPath(server: string, tool: string): string {
  return `${serverDetailPath(server, 'tools')}&tool=${encodeURIComponent(tool)}`
}

export interface FlaggedToolGroup {
  /** Bare tool name, matching `Tool.name` in the Available Tools list. */
  tool: string
  findings: SecurityScanFinding[]
  /**
   * Worst threat level present. detect only ever emits `dangerous` (any hard
   * signal) or `warning` (soft-only) — see aggregate.go — so an `info` finding
   * that somehow reached this panel buckets with the warnings rather than
   * inventing a third chip state.
   */
  level: 'dangerous' | 'warning'
  counts: { dangerous: number; warning: number; info: number }
  /** De-duplicated rule ids, in first-seen order. */
  ruleIds: string[]
}

/**
 * Group a scan report's findings by the tool they name, dropping every finding
 * whose location is not a `server:tool` pair for this server.
 *
 * Groups come back in first-seen order with dangerous groups hoisted first, so
 * the panel's reading order matches its urgency order without a second sort at
 * the call site.
 */
export function groupFindingsByTool(
  findings: SecurityScanFinding[] | null | undefined,
  expectedServer?: string | string[] | null,
): FlaggedToolGroup[] {
  const byTool = new Map<string, FlaggedToolGroup>()

  for (const finding of findings ?? []) {
    const parsed = parseToolLocation(finding.location, { expectedServer })
    if (!parsed) continue

    let group = byTool.get(parsed.tool)
    if (!group) {
      group = {
        tool: parsed.tool,
        findings: [],
        level: 'warning',
        counts: { dangerous: 0, warning: 0, info: 0 },
        ruleIds: [],
      }
      byTool.set(parsed.tool, group)
    }

    group.findings.push(finding)
    if (finding.threat_level === 'dangerous') {
      group.counts.dangerous += 1
      group.level = 'dangerous'
    } else if (finding.threat_level === 'info') {
      group.counts.info += 1
    } else {
      group.counts.warning += 1
    }

    const ruleId = finding.rule_id?.trim()
    if (ruleId && !group.ruleIds.includes(ruleId)) group.ruleIds.push(ruleId)
  }

  const groups = [...byTool.values()]
  const dangerous = groups.filter((g) => g.level === 'dangerous')
  const rest = groups.filter((g) => g.level !== 'dangerous')
  return [...dangerous, ...rest]
}
