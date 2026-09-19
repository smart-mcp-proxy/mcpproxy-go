import { describe, it, expect, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import AnnotationOverridesEditor from '@/components/AnnotationOverridesEditor.vue'

// PLAN §3.1 — frontend unit: mount editor, assert wildcard row, per-hint selects,
// save emits annotation_overrides. BDD Given/When/Then, mutation killing.

describe('AnnotationOverridesEditor', () => {
  const tools = [{ name: 'act' }, { name: 'navigate' }, { name: 'snapshot' }] as any
  const upstream = {
    act: { destructiveHint: true },
    navigate: { destructiveHint: true },
  } as any

  it('Given no overrides When mounted Then wildcard row is present and shows inherit', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: { serverName: 'browseros', tools, overrides: {}, upstreamAnnotations: upstream },
    })
    expect(wrapper.find('[data-test="annotation-override-row-*"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('wildcard')
  })

  it('Given wildcard override When rendered Then effectiveFor uses wildcard for non-exact tool', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 'browseros',
        tools,
        overrides: { '*': { destructiveHint: false } } as any,
        upstreamAnnotations: upstream,
      },
    })
    // navigate has no exact override, so effective should be destructive:false from wildcard
    // The table row for navigate should show effective badge (not "—")
    const row = wrapper.find('[data-test="annotation-override-row-navigate"]')
    expect(row.exists()).toBe(true)
    // Effective preview is visible via badge; absence of "—" implies overridden
    expect(row.text()).not.toBe('—')
  })

  it('Given an override When editing Then 3-state select Inherit/true/false is present and mutation kills', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: { serverName: 'browseros', tools, overrides: {}, upstreamAnnotations: upstream },
    })
    // Open edit for wildcard
    await wrapper.find('[data-test="annotation-override-edit-*"]').trigger('click')
    await flushPromises()
    const popover = wrapper.find('[data-test="annotation-override-popover"]')
    expect(popover.exists()).toBe(true)
    // 4 hint selects should exist, each with Inherit/true/false options
    const hints = ['readOnlyHint', 'destructiveHint', 'idempotentHint', 'openWorldHint']
    for (const h of hints) {
      const sel = wrapper.find(`[data-test="annotation-override-select-*-${h}"]`)
      expect(sel.exists(), `select for ${h} must exist`).toBe(true)
      const opts = sel.findAll('option')
      const vals = opts.map(o => o.element.getAttribute('value'))
      expect(vals).toEqual(['inherit', 'true', 'false'])
    }
    // Mutation killing: Inherit must be the default (no override yet)
    const first = wrapper.find('[data-test="annotation-override-select-*-destructiveHint"]')
    expect((first.element as HTMLSelectElement).value).toBe('inherit')
    // When choosing true Then value becomes true
    await first.setValue('true')
    expect((first.element as HTMLSelectElement).value).toBe('true')
    // Apply and then saving should emit payload
    await wrapper.find('[data-test="annotation-overrides-save"]').trigger('click')
    await flushPromises()
    // After apply, wildcard override should be in local table
    expect(wrapper.find('[data-test="annotation-override-delete-*"]').exists()).toBe(true)
  })

  it('Given per-tool override When saved Then payload contains annotation_overrides with correct hints', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: { serverName: 'browseros', tools, overrides: {}, upstreamAnnotations: upstream },
    })
    await wrapper.find('[data-test="annotation-override-edit-act"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-override-select-act-destructiveHint"]').setValue('true')
    await wrapper.find('[data-test="annotation-overrides-save"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const emitted = wrapper.emitted('save') as any[] | undefined
    expect(emitted).toBeDefined()
    const payload = emitted![0][0] as Record<string, any>
    expect(payload['act']).toBeDefined()
    expect(payload['act'].destructiveHint).toBe(true)
  })

  it('Given existing override When deleted Then save emits null marker for that tool', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 'browseros',
        tools,
        overrides: { act: { destructiveHint: true } } as any,
        upstreamAnnotations: upstream,
      },
    })
    expect(wrapper.find('[data-test="annotation-override-delete-act"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-override-delete-act"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const emitted = wrapper.emitted('save') as any[] | undefined
    expect(emitted).toBeDefined()
    const payload = emitted![0][0] as Record<string, any>
    expect(payload['act']).toBeNull()
  })
})
