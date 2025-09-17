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
})