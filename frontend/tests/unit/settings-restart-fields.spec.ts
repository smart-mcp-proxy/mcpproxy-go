import { describe, it, expect } from 'vitest'
import {
  restartRequiredLabels,
  GENERAL_FIELDS,
  SECURITY_FIELDS,
  SERVER_EDITION_FIELDS,
  ADVANCED_ACCORDIONS,
  type SettingField,
} from '@/views/settings/fields'

function allFields(): SettingField[] {
  return [
    ...GENERAL_FIELDS,
    ...SECURITY_FIELDS,
    ...SERVER_EDITION_FIELDS,
    ...ADVANCED_ACCORDIONS.flatMap((a) => a.fields),
  ]
}

function fieldByKey(key: string): SettingField | undefined {
  return allFields().find((f) => f.key === key)
}

describe('Settings restart-required surface (UX audit F16)', () => {
  it('derives the hint list from the fields that actually carry the badge', () => {
    const labels = restartRequiredLabels()
    const badged = allFields().filter((f) => f.restart)

    expect(badged.length).toBeGreaterThan(0)
    for (const f of badged) {
      expect(labels).toContain(f.label)
    }
    // Nothing in the list that no field claims — the old hardcoded prose listed
    // "Data directory", which has no settings field at all.
    for (const label of labels) {
      expect(badged.some((f) => f.label === label)).toBe(true)
    }
  })

  it('does not badge enable_code_execution — it hot-applies', () => {
    const field = fieldByKey('enable_code_execution')
    expect(field).toBeDefined()
    expect(field!.restart).toBeFalsy()
    expect(restartRequiredLabels()).not.toContain(field!.label)
  })

  it('badges code_execution_pool_size — the JS runtime pool is sized at startup', () => {
    const field = fieldByKey('code_execution_pool_size')
    expect(field).toBeDefined()
    expect(field!.restart).toBe(true)
    expect(restartRequiredLabels()).toContain(field!.label)
  })

  it('leaves the hot-reloadable code-execution limits unbadged', () => {
    for (const key of [
      'code_execution_timeout_ms',
      'code_execution_max_tool_calls',
      'code_execution_max_parallel',
    ]) {
      expect(fieldByKey(key)?.restart).toBeFalsy()
    }
  })
})
