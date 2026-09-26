import { describe, it, expect, vi, beforeEach } from 'vitest'

vi.mock('@/services/api', () => ({
  default: {
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
  },
}))
import api from '@/services/api'
import { resolveSecretFields } from '@/composables/useSecretFields'

describe('resolveSecretFields', () => {
  beforeEach(() => {
    vi.mocked(api.getSecretRefs).mockReset()
    vi.mocked(api.setSecret).mockReset()
    vi.mocked(api.deleteSecret).mockReset()
  })

  it('passes value-mode fields through unchanged without ever calling getSecretRefs', async () => {
    const result = await resolveSecretFields('srv', [
      { kind: 'env', name: 'PORT', value: '8080', mode: 'value' },
    ])
    expect(result).toEqual({ env: { PORT: '8080' }, headers: {}, writtenRefs: [] })
    expect(api.getSecretRefs).not.toHaveBeenCalled()
  })

  // Review round 4 (F-C, Web variant): a transient failure of GET
  // /secrets/refs must NOT be treated as "nothing is taken" — that would let
  // a computed ref name collide with (and, via a later rollback, DESTROY) a
  // genuinely pre-existing keyring entry for an already-configured, in-use
  // server. It must abort instead, writing nothing.
  it('aborts without writing any secret when GET /secrets/refs fails transiently', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: false, error: 'upstream timeout' })

    await expect(
      resolveSecretFields('github', [
        { kind: 'env', name: 'TOKEN', value: 'sk-live-abc', mode: 'secret' },
      ])
    ).rejects.toThrow(/upstream timeout|Failed to check existing secret names/)

    expect(api.setSecret).not.toHaveBeenCalled()
  })

  it('suffixes a colliding ref name instead of overwriting the existing one', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({
      success: true,
      data: { refs: [{ name: 'github-env-token', type: 'keyring' }] },
    })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })

    const result = await resolveSecretFields('github', [
      { kind: 'env', name: 'TOKEN', value: 'sk-live-abc', mode: 'secret' },
    ])

    expect(api.setSecret).toHaveBeenCalledWith('github-env-token-2', 'sk-live-abc')
    expect(result.env.TOKEN).toBe('${keyring:github-env-token-2}')
    expect(result.writtenRefs).toEqual(['github-env-token-2'])
  })
})
