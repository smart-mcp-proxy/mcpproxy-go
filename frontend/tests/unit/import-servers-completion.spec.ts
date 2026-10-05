import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ImportServers from '@/components/ImportServers.vue'
import api from '@/services/api'

// Spec 109 US7-4 / FR-043 (fix-usertest-web T200, audit F-04): after an import
// the detected importer shows ONE completion line. It used to render
// "No importable servers found" directly above "2 servers imported · 1 skipped
// (not selected)" because the reload after the import found everything already
// on this instance.

vi.mock('@/services/api', () => ({
  default: {
    getCanonicalConfigPaths: vi.fn(),
    importServersFromJSON: vi.fn(),
    importServersFromPath: vi.fn(),
  },
}))

const cursorPath = { name: 'Cursor', format: 'cursor', path: '/tmp/cursor.json', exists: true }

function mockFirstPreviewThenAlreadyExists() {
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [cursorPath] } })
  ;(api.importServersFromPath as any)
    // first preview: two importable servers
    .mockResolvedValueOnce({
      success: true,
      data: { imported: [{ name: 'fetchy', summary: 'node a' }, { name: 'thinker', summary: 'node b' }] },
    })
    // apply
    .mockResolvedValueOnce({
      success: true,
      data: { summary: { imported: 2 }, imported: [], skipped: [{ name: 'mcpproxy', reason: 'filtered_out' }] },
    })
    // reload preview: both now already exist
    .mockResolvedValueOnce({
      success: true,
      data: {
        imported: [],
        skipped: [
          { name: 'fetchy', reason: 'already_exists' },
          { name: 'thinker', reason: 'already_exists' },
        ],
      },
    })
}

async function importBoth(props: Record<string, unknown>) {
  mockFirstPreviewThenAlreadyExists()
  const wrapper = mount(ImportServers, { props: { detected: true, ...props } })
  await flushPromises()
  await wrapper.find('[data-test="select-all-cursor"]').setValue(true)
  await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('ImportServers detected completion state', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows one completion line and no skip-not-selected or empty text after an import', async () => {
    const wrapper = await importBoth({ showEmpty: false })
    const message = wrapper.find('[data-test="detected-import-message"]')
    expect(message.text()).toBe('✓ 2 servers imported')
    expect(message.text()).not.toContain('skipped')
    expect(wrapper.find('[data-test="detected-import-empty"]').exists()).toBe(false)
    expect(wrapper.emitted('imported')?.[0]).toEqual([2, ['fetchy', 'thinker']])
  })

  it('renders nothing for the empty state when showEmpty is false and nothing is detected at mount', async () => {
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    const wrapper = mount(ImportServers, { props: { detected: true, showEmpty: false } })
    await flushPromises()
    expect(wrapper.find('[data-test="detected-import-empty"]').exists()).toBe(false)
  })

  it('keeps the standalone first-load empty copy by default', async () => {
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    const wrapper = mount(ImportServers, { props: { detected: true } })
    await flushPromises()
    expect(wrapper.find('[data-test="detected-import-empty"]').text()).toBe('No importable servers found in local client configs.')
  })

  it('says nothing is left, not that nothing was found, after an import that leaves nothing', async () => {
    const wrapper = await importBoth({})
    expect(wrapper.find('[data-test="detected-import-empty"]').text()).toBe(
      'Nothing left to import — every server in your client configs is on MCPProxy.',
    )
    expect(wrapper.find('[data-test="detected-import-message"]').text()).toBe('✓ 2 servers imported')
  })
})

describe('ImportServers paste import count (#1466)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('emits the number the backend actually imported, not the number selected', async () => {
    ;(api.importServersFromJSON as any)
      .mockResolvedValueOnce({
        success: true,
        data: { imported: [{ name: 'a' }, { name: 'b' }] },
      })
      .mockResolvedValueOnce({
        success: true,
        data: { summary: { imported: 1 }, imported: [{ name: 'a' }], skipped: [{ name: 'b', reason: 'already_exists' }] },
      })
    const wrapper = mount(ImportServers, { props: { detected: false } })
    await wrapper.find('[data-test="import-content-textarea"]').setValue('{"mcpServers":{}}')
    await wrapper.find('[data-test="import-preview-button"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="import-confirm-button"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('imported')?.[0]).toEqual([1, ['a', 'b']])
  })
})
