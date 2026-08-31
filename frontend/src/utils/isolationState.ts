import type { Server, ServerIsolationEffective } from '../types/api'

/**
 * A human-readable account of a server's isolation state.
 *
 * `label` answers "is it isolated"; `detail` answers "why", which is the part
 * a bare toggle could never express. Before GH #1142 the API reported the raw
 * per-server override flattened to a bool, so a server inheriting the global
 * setting looked identical to one explicitly opted out — the UI had no way to
 * tell the difference and showed containerised servers as unisolated.
 */
export interface IsolationDescription {
  isolated: boolean
  /** e.g. "Isolated (docker)" / "Not isolated" */
  label: string
  /** e.g. "Inherits the global setting (docker)" */
  detail: string
}

function modeLabel(mode: string | undefined): string {
  return mode && mode !== 'none' ? mode : 'none'
}

/**
 * Describes the resolved isolation state, or null when the backend sent no
 * resolution block (an older core, or a server with no local process).
 *
 * Unknown `source` values degrade to the inherit story rather than to a wrong
 * claim — the vocabulary is documented as extensible.
 */
export function describeIsolation(server: Server | null | undefined): IsolationDescription | null {
  const eff: ServerIsolationEffective | undefined = server?.isolation_effective
  if (!eff) return null

  const globalMode = modeLabel(eff.global_mode)
  const label = eff.isolated ? `Isolated (${modeLabel(eff.mode)})` : 'Not isolated'

  let detail: string
  switch (eff.source) {
    case 'server-mode':
      detail = `Mode set for this server: ${modeLabel(eff.mode)}`
      break
    case 'server-opt-out':
      detail = `Turned off for this server (global setting is ${globalMode})`
      break
    case 'server-opt-in-ignored':
      detail = 'Turned on for this server, but global isolation is off — the setting is ignored'
      break
    case 'not-stdio':
      detail = 'No local process to isolate'
      break
    case 'already-docker':
      detail = 'This server already runs Docker itself'
      break
    default:
      // 'global' and anything unrecognized.
      detail = eff.inherited
        ? `Inherits the global setting (${globalMode})`
        : 'Turned on for this server'
      break
  }

  return { isolated: eff.isolated, label, detail }
}
