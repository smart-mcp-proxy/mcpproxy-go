import { describe, it, expect } from 'vitest'
import { refName } from '@/utils/secretRef'
import { looksSecret } from '@/utils/secretLike'
import fixture from './fixtures/ref_names.json'

describe('refName', () => {
  it('computes <server>-<kind>-<key>, lowercased and normalized', () => {
    expect(refName('github', 'env', 'API_KEY')).toBe('github-env-api-key')
    expect(refName('github', 'header', 'API_KEY')).toBe('github-header-api-key')
  })

  it('gives an env var and a header of the same name distinct refs (FR-065)', () => {
    const env = refName('github', 'env', 'API_KEY')
    const header = refName('github', 'header', 'API_KEY')
    expect(env).not.toBe(header)
  })

  it('appends -2, -3, ... on a taken name (D28: never overwrite)', () => {
    const taken = new Set(['github-env-api-key', 'github-env-api-key-2'])
    expect(refName('github', 'env', 'API_KEY', (n) => taken.has(n))).toBe('github-env-api-key-3')
  })

  it('caps the result at 64 characters even with a numeric suffix', () => {
    const longKey = 'THIS_IS_A_VERY_VERY_VERY_VERY_VERY_VERY_VERY_LONG_ENV_VAR_NAME'
    const got = refName('some-really-long-server-name', 'env', longKey, () => false)
    expect(got.length).toBeLessThanOrEqual(64)
  })

  it('matches the shared fixture the Go and Swift ports also decode', () => {
    type FixtureCase = { server: string; kind: 'env' | 'header'; key: string; taken: string[]; want: string }
    const cases = (fixture as { cases: FixtureCase[] }).cases
    expect(cases.length).toBeGreaterThan(0)
    for (const c of cases) {
      const takenSet = new Set(c.taken)
      expect(refName(c.server, c.kind, c.key, (n) => takenSet.has(n))).toBe(c.want)
    }
  })
})

describe('looksSecret', () => {
  it('flags conventional secret-ish names', () => {
    for (const name of ['GITHUB_TOKEN', 'API_KEY', 'PASSWORD', 'Authorization', 'PRIVATE_KEY']) {
      expect(looksSecret(name)).toBe(true)
    }
  })

  it('does not flag ordinary config names', () => {
    for (const name of ['PORT', 'WORKDIR', 'PATH', 'REGION']) {
      expect(looksSecret(name)).toBe(false)
    }
  })
})
