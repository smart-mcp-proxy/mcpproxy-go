import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import CredentialOnceDialog from '@/components/clients/CredentialOnceDialog.vue'

describe('CredentialOnceDialog public MCP endpoint', () => {
  it('shows and copies the browser-origin URL while retaining the issued header', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    const config = {
      mcpServers: {
        mcpproxy: {
          url: 'http://10.0.0.5:8080/mcp/p/review?mode=read',
          headers: { 'X-API-Key': 'one-time-credential' },
        },
      },
    }
    const wrapper = mount(CredentialOnceDialog, {
      props: {
        open: true,
        credential: 'one-time-credential',
        snippet: { generic_http: JSON.stringify(config), header_name: 'X-API-Key' },
      },
      global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="actions" /></div>' } } },
    })

    // Vue binds textarea value as a property; inspect the rendered element.
    const rendered = JSON.parse((wrapper.get('[data-test="credential-once-snippet"]').element as HTMLTextAreaElement).value)
    expect(rendered.mcpServers.mcpproxy.url).toBe(`${window.location.origin}/mcp/p/review?mode=read`)
    expect(rendered.mcpServers.mcpproxy.headers).toEqual(config.mcpServers.mcpproxy.headers)
    await wrapper.get('[data-test="credential-once-copy-snippet"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith(JSON.stringify(rendered, null, 2))
  })
})
