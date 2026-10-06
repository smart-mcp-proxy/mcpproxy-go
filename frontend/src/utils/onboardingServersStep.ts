// Pure view-state and copy helpers for the setup wizard's Servers step and the
// import summary line (Spec 109 US7-4 / FR-043). Kept out of the components so
// the completion rules are unit-testable without mounting a dialog.
import { skipReasonLabel } from '@/utils/importSkipReason'

// Skip reasons that describe the user's own choice (an unchecked server, or the
// self entry filtered before the self-reference check), not an outcome. They
// are never reported in a Web import summary.
export const UNREPORTED_SKIP_REASONS = new Set(['filtered_out'])

export type ServersStepView = 'choose' | 'review' | 'imported' | 'empty'

export interface ServersStepInput {
  sourcesWithServers: number
  hasUsableServer: boolean
  awaitingReview: number
  importedThisSession: number
}

/**
 * Which body the Servers step shows.
 *  - choose: there is something left to import.
 *  - review: nothing left, no usable server, imported servers wait in quarantine.
 *  - imported: nothing left, and this session imported something that is either
 *    usable already or has nothing waiting — a completion state, not "nothing".
 *  - empty: nothing to import and nothing imported this session.
 */
export function serversStepView(input: ServersStepInput): ServersStepView {
  if (input.sourcesWithServers > 0) return 'choose'
  if (!input.hasUsableServer && input.awaitingReview > 0) return 'review'
  if (input.importedThisSession > 0 && (input.hasUsableServer || input.awaitingReview === 0)) return 'imported'
  return 'empty'
}

/**
 * How many of the servers imported this wizard session are still waiting in
 * quarantine. An import that was not quarantined (or has since been approved)
 * must not be reported as "including the N you just imported" (#1466).
 */
export function countImportedStillQuarantined(
  importedNames: Iterable<string>,
  quarantinedNames: Iterable<string>,
): number {
  const quarantined = new Set(quarantinedNames)
  let n = 0
  for (const name of new Set(importedNames)) if (quarantined.has(name)) n++
  return n
}

/** The one-sentence status under "Approve a server to finish this step.". */
export function awaitingReviewSentence(awaiting: number, justImported: number): string {
  const plural = awaiting !== 1
  const tail = `review ${plural ? 'their' : 'its'} tools before ${plural ? 'they' : 'it'} can run.`
  if (justImported > 0 && justImported >= awaiting) {
    return awaiting === 1
      ? `The server you just imported is waiting in quarantine — ${tail}`
      : `The ${awaiting} servers you just imported are waiting in quarantine — ${tail}`
  }
  const head = `${awaiting} server${plural ? 's are' : ' is'} waiting in quarantine`
  if (justImported > 0) {
    const mine = justImported === 1 ? 'the one' : `the ${justImported}`
    return `${head}, including ${mine} you just imported — ${tail}`
  }
  return `${head} — ${tail}`
}

export interface ImportSummaryInput {
  imported: number
  renamed?: number
  skipped?: Array<{ reason?: string | null }>
}

/** "2 servers imported · 1 renamed · 1 skipped (already configured)". */
export function importSummary(input: ImportSummaryInput): string {
  const parts = [input.imported > 0 ? `${input.imported} server${input.imported === 1 ? '' : 's'} imported` : 'No servers imported']
  if (input.renamed) parts.push(`${input.renamed} renamed`)
  const byReason = new Map<string, number>()
  for (const skipped of input.skipped ?? []) {
    const reason = skipped.reason ?? ''
    if (UNREPORTED_SKIP_REASONS.has(reason)) continue
    byReason.set(reason, (byReason.get(reason) ?? 0) + 1)
  }
  for (const [reason, count] of byReason) parts.push(`${count} skipped (${skipReasonLabel(reason)})`)
  return parts.join(' · ')
}
