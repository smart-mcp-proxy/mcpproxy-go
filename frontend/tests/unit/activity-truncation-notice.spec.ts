import { describe, it, expect } from 'vitest'
import {
  responseTruncationNotice,
  TRUNCATION_NOTICE_UNRESOLVED,
} from '@/utils/activity'

// The Web UI must not hold a copy of the truncation direction table (#1173).
//
// What `response_truncated` and `response_storage_truncated` mean depends on the
// record type AND on whether both are set. Activity.vue used to encode that in
// two per-badge tooltips, and a per-badge tooltip can only see one flag — so a
// reader hovering "Truncated" on a both-flags record was told the stored body is
// the agent's own copy when it is strictly shorter than it, with no correction
// anywhere in that tooltip.
//
// The fix: the backend resolves the cell once
// (`contracts.ResolveResponseTruncation`, pinned across all 3 types x 4 flag
// combinations in internal/contracts/activity_truncation_test.go) and ships the
// answer as `response_truncation_notice`. These tests pin the UI to CHOOSING
// that string — never to deriving one.
describe('responseTruncationNotice', () => {
  it('renders the backend-resolved sentence verbatim', () => {
    const resolved =
      'The agent received MORE than this: the upstream response was cut to tool_response_limit ' +
      'before being forwarded and recorded, and the recorded copy was then shortened AGAIN to ' +
      'fit activity_max_response_size.'

    expect(
      responseTruncationNotice({
        type: 'tool_call',
        response_truncated: true,
        response_storage_truncated: true,
        response_truncation_notice: resolved,
      } as never)
    ).toBe(resolved)
  })

  it('gives BOTH badges the same string, so neither can contradict the other', () => {
    const record = {
      type: 'tool_call',
      response_truncated: true,
      response_storage_truncated: true,
      response_truncation_notice: 'resolved sentence for this cell',
    } as never

    // Activity.vue binds one computed to both badge titles; this is the
    // property that makes that safe.
    const forwardBadgeTitle = responseTruncationNotice(record)
    const storageBadgeTitle = responseTruncationNotice(record)
    expect(forwardBadgeTitle).toBe(storageBadgeTitle)
  })

  it('never derives a direction from the flags themselves', () => {
    // Identical flags, different backend answers: the helper must follow the
    // backend, not the booleans. If it ever re-derived, these two would collapse
    // to one string.
    const flags = { type: 'tool_call', response_truncated: true, response_storage_truncated: true }
    expect(responseTruncationNotice({ ...flags, response_truncation_notice: 'A' } as never)).toBe('A')
    expect(responseTruncationNotice({ ...flags, response_truncation_notice: 'B' } as never)).toBe('B')
  })

  it('claims NO direction when an older core sent no resolved sentence', () => {
    // An absent field is exactly the case where this UI cannot know which side
    // of the cut the record holds. Guessing is the bug; the fallback states the
    // fact and points at the CLI, which resolves the cell locally.
    for (const record of [
      { type: 'tool_call', response_truncated: true },
      { type: 'internal_tool_call', response_truncated: true, response_storage_truncated: true },
      { type: 'prompt_get', response_storage_truncated: true, response_truncation_notice: '' },
    ]) {
      const notice = responseTruncationNotice(record as never)
      expect(notice).toBe(TRUNCATION_NOTICE_UNRESOLVED)
      expect(notice.toLowerCase()).not.toContain('more than this')
      expect(notice.toLowerCase()).not.toContain('less than this')
      expect(notice.toLowerCase()).not.toContain("agent's own copy")
    }
  })

  it("never calls a both-flags tool_call record the agent's own copy", () => {
    // The blocking finding, as an outcome. activity_service.go cuts the
    // already-forwarded text again, so the stored body is STRICTLY shorter than
    // what the agent received. Whatever reaches the badge — resolved sentence or
    // fallback — must not assert otherwise.
    const backendAnswer =
      'The agent received MORE than this: the upstream response was cut to tool_response_limit ' +
      'before being forwarded and recorded, and the recorded copy was then shortened AGAIN to ' +
      'fit activity_max_response_size.'

    for (const notice of [
      responseTruncationNotice({
        type: 'tool_call',
        response_truncated: true,
        response_storage_truncated: true,
        response_truncation_notice: backendAnswer,
      } as never),
      responseTruncationNotice({
        type: 'tool_call',
        response_truncated: true,
        response_storage_truncated: true,
      } as never),
    ]) {
      expect(notice).not.toContain("agent's own copy")
      expect(notice).not.toContain('This is the agent')
    }
  })
})
