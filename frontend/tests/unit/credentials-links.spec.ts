import { describe, expect, it } from 'vitest'
import router from '@/router'

// Spec 115 T070 (UI-007): every `links` target of a create result resolves in
// the router to the filtered view.
describe('credential deep links resolve', () => {
  const cases: Array<[string, string, Record<string, string>]> = [
    ['/clients?client=delegated-worker', 'clients', { client: 'delegated-worker' }],
    ['/clients?tab=tokens&token=research-task-42', 'clients', { tab: 'tokens', token: 'research-task-42' }],
    ['/profiles/daily-research?tab=tools&reason=callable', 'profile-editor', { tab: 'tools', reason: 'callable' }],
    ['/activity?client=delegated-worker', 'activity', { client: 'delegated-worker' }],
    ['/activity?token=research-task-42', 'activity', { token: 'research-task-42' }],
  ]
  for (const [path, name, query] of cases) {
    it(path, () => {
      const resolved = router.resolve(path)
      expect(resolved.matched.length).toBeGreaterThan(0)
      if (name !== 'activity') expect(resolved.name).toBe(name)
      for (const [k, v] of Object.entries(query)) expect(resolved.query[k]).toBe(v)
      expect(resolved.fullPath.toLowerCase()).not.toContain('apikey')
    })
  }
})
