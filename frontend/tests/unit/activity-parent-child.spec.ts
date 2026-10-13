import { describe, it, expect } from 'vitest'
import { isChildCall, isCodeExecutionActivity } from '../../src/utils/activity'

// Activity transparency: a `code_execution` call fans out into N sandboxed
// sub-calls. The parent keeps ONE internal_tool_call record (request_id =
// parentCallID); every sub-call gets its own tool_call record carrying
// parent_id = parentCallID. The Web UI has to be able to recognise both ends of
// that link from a record alone, without re-deriving the rule in two templates.

describe('isCodeExecutionActivity', () => {
  it('is true for an internal_tool_call whose metadata names code_execution', () => {
    expect(
      isCodeExecutionActivity({
        type: 'internal_tool_call',
        metadata: { internal_tool_name: 'code_execution' },
      })
    ).toBe(true)
  })

  it('is true for an internal_tool_call whose tool_name is code_execution', () => {
    // Older records (and the CLI surface) put the built-in name in tool_name.
    expect(
      isCodeExecutionActivity({ type: 'internal_tool_call', tool_name: 'code_execution' })
    ).toBe(true)
  })

  it('is false for another internal tool', () => {
    expect(
      isCodeExecutionActivity({
        type: 'internal_tool_call',
        metadata: { internal_tool_name: 'retrieve_tools' },
      })
    ).toBe(false)
  })

  it('is false for an upstream tool_call, even one literally named code_execution', () => {
    // A rogue upstream server may expose a tool called "code_execution"; only
    // the built-in (internal_tool_call) is the sandbox parent.
    expect(isCodeExecutionActivity({ type: 'tool_call', tool_name: 'code_execution' })).toBe(false)
  })

  it('is false for missing / empty input', () => {
    expect(isCodeExecutionActivity(null)).toBe(false)
    expect(isCodeExecutionActivity(undefined)).toBe(false)
    expect(isCodeExecutionActivity({})).toBe(false)
  })
})

describe('isChildCall', () => {
  it('is true when the record carries a parent_id', () => {
    expect(isChildCall({ parent_id: 'req-parent-1' })).toBe(true)
  })

  it('is false for a top-level record', () => {
    expect(isChildCall({ type: 'tool_call' })).toBe(false)
  })

  it('is false for an empty parent_id (omitempty absent field, never a blank link)', () => {
    expect(isChildCall({ parent_id: '' })).toBe(false)
  })

  it('is false for missing input', () => {
    expect(isChildCall(null)).toBe(false)
    expect(isChildCall(undefined)).toBe(false)
  })
})
