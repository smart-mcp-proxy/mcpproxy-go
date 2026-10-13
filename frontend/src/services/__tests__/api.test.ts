import { describe, it, expect, beforeEach, vi } from 'vitest'
import apiService from '../api'

// Mock fetch globally
global.fetch = vi.fn()

describe('APIService', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('should make GET request to servers endpoint', async () => {
    const mockResponse = {
      success: true,
      data: { servers: [] }
    }

    ;(global.fetch as any).mockResolvedValueOnce({
      ok: true,
      json: async () => mockResponse
    })

    const result = await apiService.getServers()

    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/servers',
      expect.objectContaining({
        headers: expect.objectContaining({
          'Content-Type': 'application/json'
        })
      })
    )
    expect(result).toEqual(mockResponse)
  })

  it('should handle API errors correctly', async () => {
    ;(global.fetch as any).mockResolvedValueOnce({
      ok: false,
      status: 404,
      statusText: 'Not Found'
    })

    const result = await apiService.getServers()
    expect(result.success).toBe(false)
    expect(result.error).toContain('HTTP 404')
  })

  // Profiles v2 (MCP-3243 / T4): the switcher consumes the REST surface from
  // MCP-3241.
  it('getProfiles GETs /api/v1/profiles', async () => {
    const mockResponse = {
      success: true,
      data: { profiles: [{ name: 'dev', servers: ['github'], tool_count: 3 }] }
    }
    ;(global.fetch as any).mockResolvedValueOnce({ ok: true, json: async () => mockResponse })

    const result = await apiService.getProfiles()
    expect(global.fetch).toHaveBeenCalledWith('/api/v1/profiles', expect.any(Object))
    expect(result).toEqual(mockResponse)
  })

  it('setActiveProfile PUTs the slug to /api/v1/profiles/active', async () => {
    const mockResponse = { success: true, data: { active_profile: 'dev' } }
    ;(global.fetch as any).mockResolvedValueOnce({ ok: true, json: async () => mockResponse })

    const result = await apiService.setActiveProfile('dev')
    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/profiles/active',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ profile: 'dev' }) })
    )
    expect(result).toEqual(mockResponse)
  })

  it('setActiveProfile sends an empty slug to clear the active profile', async () => {
    ;(global.fetch as any).mockResolvedValueOnce({
      ok: true,
      json: async () => ({ success: true, data: { active_profile: '' } })
    })

    await apiService.setActiveProfile('')
    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/profiles/active',
      expect.objectContaining({ method: 'PUT', body: JSON.stringify({ profile: '' }) })
    )
  })
})
