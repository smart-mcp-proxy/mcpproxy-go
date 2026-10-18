import { describe, expect, it } from 'vitest'
import { browserMcpEndpoint, browserMcpSnippet } from '@/utils/mcpEndpoint'

describe('browser MCP endpoint delivery', () => {
  it.each([
    ['https://proxy.example.com', 'https://proxy.example.com/mcp'],
    ['http://127.0.0.1:8080', 'http://127.0.0.1:8080/mcp'],
    ['https://[::1]:8443', 'https://[::1]:8443/mcp'],
  ])('uses the visible origin %s, including its scheme and port', (origin, expected) => {
    expect(browserMcpEndpoint('/mcp', origin)).toBe(expected)
  })

  it('preserves profile endpoint paths, query values, and fragments', () => {
    const origin = 'https://proxy.example.com:8443'
    expect(browserMcpEndpoint('http://10.0.0.5:8080/mcp/p/review?mode=read#tools', origin))
      .toBe(`${origin}/mcp/p/review?mode=read#tools`)
  })

  it('rewrites only the one-time snippet URL and preserves headers and credentials', () => {
    const config = {
      mcpServers: {
        mcpproxy: {
          url: 'http://10.0.0.5:8080/mcp/p/review?mode=read',
          headers: { 'X-API-Key': 'test-credential' },
        },
        otherServer: { url: 'https://other.example/mcp' },
      },
    }
    const result = JSON.parse(browserMcpSnippet(JSON.stringify(config), 'https://proxy.example.com'))
    expect(result.mcpServers.mcpproxy).toEqual({
      ...config.mcpServers.mcpproxy,
      url: 'https://proxy.example.com/mcp/p/review?mode=read',
    })
    expect(result.mcpServers.otherServer).toEqual(config.mcpServers.otherServer)
  })

  it('returns invalid snippets unchanged so their contents are not lost', () => {
    expect(browserMcpSnippet('invalid', 'https://proxy.example.com')).toBe('invalid')
  })
})
