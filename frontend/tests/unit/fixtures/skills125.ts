import type { SkillEntry, SkillFileInfo, SkillsListResponse } from '@/types/api'

export const skillFile = (path: string, digest = `sha256:${path}`, size = 20, text = true): SkillFileInfo => ({ path, size, digest, text })

export const makeSkill = (path: string, over: Partial<SkillEntry> = {}): SkillEntry => ({
  id: `srv:${path}`, uri: `skill://${path}/SKILL.md`, path, name: path, description: `desc of ${path}`,
  status: 'pending', blocked: false, listed: true, available: false, unusable_reason: '',
  current_hash: `sk1-${path}`, approved_hash: '', approved_by: '',
  files: [skillFile('SKILL.md')], total_size: 20,
  scan: { verdict: 'clean', complete: true, incomplete: '', findings_truncated: false, findings: [] },
  ...over,
})

export const makeListing = (skills: SkillEntry[], over: Partial<SkillsListResponse> = {}): SkillsListResponse => ({
  enabled: true, incarnation: 7, server: 'srv', supported: true, expose_skills: null,
  last_refresh: '2026-11-16T19:20:00Z', error: '', truncated: false, skills, ...over,
})

export const ok = <T,>(data: T) => ({ success: true, data })
