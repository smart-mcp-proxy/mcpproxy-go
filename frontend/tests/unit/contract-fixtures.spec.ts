import { existsSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import * as contracts from '@/types/contracts'
import type { AccessExplanation, ClientView, EffectiveTool, ProfileConfig } from '@/types/api'

// Spec 108-i T096 / FR-052: the Go contract fixtures (T003) are the one source of
// truth for the wire shapes. This decodes every one of them against the
// hand-written TypeScript types with a runtime key-and-enum check, so a field
// renamed on one side and not the other fails here.

const DIR = resolve(__dirname, '../../../internal/profile/testdata/contract')
const read = (name: string) => JSON.parse(readFileSync(resolve(DIR, name), 'utf8'))

// The enum values the generator exports (internal/profile/contract.go).
function enumValues(prefix: string, exclude: string[] = []): string[] {
  return Object.entries(contracts)
    .filter(([key, value]) => key.startsWith(prefix) && !exclude.some(skip => key.startsWith(skip)) && typeof value === 'string')
    .map(([, value]) => value as string)
}
const REASONS = enumValues('ProfileReason')
const STEPS = enumValues('ExplainStep', ['ExplainStepStatus'])
const STEP_STATUS = enumValues('ExplainStepStatus')
const FIX_ACTIONS = enumValues('FixAction')
const VERDICTS = enumValues('ExplainVerdict')
const CREDENTIAL_STATES = enumValues('CredentialState')
const SOURCES = enumValues('ProfileSource')
const SUBJECTS = enumValues('AccessSubject')
const BLOCK_REASONS = enumValues('BlockReasonProfile')

const PROFILE_CONFIG_KEYS = ['name', 'servers', 'title', 'description', 'max_tier', 'unannotated', 'tools', 'code_execution', 'management_tools', 'switchable_to']
const TOOL_RULE_KEYS = ['allow', 'deny', 'classify']
const EFFECTIVE_TOOL_KEYS = ['server', 'tool', 'intrinsic_tier', 'profile_tier', 'access', 'classification_stale']
const CLIENT_ROW_KEYS = ['id', 'display_name', 'kind', 'credential_state', 'token_name', 'profile', 'profile_title', 'profile_mode', 'profile_source', 'profile_missing', 'expires_at', 'rotation_pending', 'blocked_24h', 'warnings']

function onlyKnownKeys(value: Record<string, unknown>, allowed: string[]) {
  expect(Object.keys(value).filter(key => !allowed.includes(key))).toEqual([])
}

describe('contract fixtures decode against the TypeScript types (Spec 108-i T096)', () => {
  it('profile_full.json and profile_legacy.json are ProfileConfig documents', () => {
    for (const name of ['profile_full.json', 'profile_legacy.json']) {
      const cfg = read(name) as ProfileConfig
      onlyKnownKeys(cfg as any, PROFILE_CONFIG_KEYS)
      expect(typeof cfg.name).toBe('string')
      expect(Array.isArray(cfg.servers)).toBe(true)
      if (cfg.tools) onlyKnownKeys(cfg.tools as any, TOOL_RULE_KEYS)
      if (cfg.max_tier) expect(['read', 'write', 'destructive']).toContain(cfg.max_tier)
      if (cfg.unannotated) expect(['deny', 'as_write', 'as_read']).toContain(cfg.unannotated)
      for (const tier of Object.values(cfg.tools?.classify ?? {})) expect(['read', 'write', 'destructive']).toContain(tier)
    }
    // The legacy document sets none of the six policy fields.
    const legacy = read('profile_legacy.json')
    for (const key of ['max_tier', 'unannotated', 'tools', 'code_execution', 'management_tools', 'switchable_to']) expect(key in legacy).toBe(false)
  })

  it('effective_tools.json rows are EffectiveTool with a known reason', () => {
    const rows = read('effective_tools.json') as EffectiveTool[]
    expect(rows.length).toBeGreaterThan(0)
    for (const row of rows) {
      onlyKnownKeys(row as any, EFFECTIVE_TOOL_KEYS)
      for (const key of EFFECTIVE_TOOL_KEYS) expect(key in row).toBe(true)
      expect(REASONS).toContain(row.access.reason)
      expect(typeof row.access.visible).toBe('boolean')
      expect(typeof row.access.callable).toBe('boolean')
      expect(typeof row.classification_stale).toBe('boolean')
    }
  })

  it('client_rows.json rows carry the ClientView credential and binding fields', () => {
    const rows = read('client_rows.json') as Array<ClientView & { warnings: string[] }>
    for (const row of rows) {
      onlyKnownKeys(row as any, CLIENT_ROW_KEYS)
      expect(CREDENTIAL_STATES).toContain(row.credential_state)
      if (row.profile_source) expect(SOURCES).toContain(row.profile_source)
      if (row.profile_mode) expect(['locked', 'switchable']).toContain(row.profile_mode)
      expect(typeof row.blocked_24h).toBe('number')
    }
  })

  it('explain_blocked.json is an F24 AccessExplanation', () => {
    const explanation = read('explain_blocked.json') as AccessExplanation
    // The F24 shape: subject {kind, name}, profile {name, source}; never the pre-F24 {client}.
    expect(Object.keys(explanation.subject).sort()).toEqual(['kind', 'name'])
    expect(SUBJECTS).toContain(explanation.subject.kind)
    expect(SOURCES).toContain(explanation.profile.source)
    expect(VERDICTS).toContain(explanation.verdict)
    expect(explanation.steps.map(step => step.step)).toEqual(STEPS)
    for (const step of explanation.steps) expect(STEP_STATUS).toContain(step.status)
    expect(STEPS).toContain(explanation.first_failure)
    expect(explanation.steps.find(step => step.step === explanation.first_failure)?.status).toBe('fail')
    for (const fix of explanation.fixes) {
      expect(FIX_ACTIONS).toContain(fix.action)
      expect(STEPS).toContain(fix.step)
      expect(typeof fix.label).toBe('string')
    }
    // A visibility failure is reported as hidden, never blocked.
    expect(explanation.verdict).toBe('hidden')
  })

  it('activity_attributed.json carries the FR-029 attribution fields', () => {
    const record = read('activity_attributed.json')
    expect(SOURCES).toContain(record.profile_source)
    expect(BLOCK_REASONS).toContain(record.block_reason)
    for (const key of ['profile', 'client_id', 'client_name', 'token_name']) expect(typeof record[key]).toBe('string')
  })

  it('also decodes 108-f\'s access_explanation.json when it ships one', () => {
    const path = resolve(__dirname, '../../../specs/108-profiles-v3/contracts/fixtures/access_explanation.json')
    if (!existsSync(path)) return
    const explanation = JSON.parse(readFileSync(path, 'utf8')) as AccessExplanation
    expect(VERDICTS).toContain(explanation.verdict)
  })
})
