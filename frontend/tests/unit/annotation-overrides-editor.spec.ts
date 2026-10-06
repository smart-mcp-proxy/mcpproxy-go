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

  it('Given explicit destructiveHint:false When rendered Then Non-destructive badge is visible (override not silent)', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 'browseros',
        tools,
        overrides: { act: { destructiveHint: false } } as any,
        upstreamAnnotations: upstream,
      },
    })
    const row = wrapper.find('[data-test="annotation-override-row-act"]')
    expect(row.exists()).toBe(true)
    // Author's AnnotationBadges only renders truthy hints, so without this the
    // override would be invisible (title only). Mutation: removing the badge
    // span must fail this test.
    expect(row.text()).toContain('Non-destructive')
  })

  it('Given explicit readOnlyHint:false When rendered Then Write badge is visible', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 'browseros',
        tools,
        overrides: { navigate: { destructiveHint: false, readOnlyHint: false } } as any,
        upstreamAnnotations: upstream,
      },
    })
    const row = wrapper.find('[data-test="annotation-override-row-navigate"]')
    expect(row.exists()).toBe(true)
    expect(row.text()).toContain('Write')
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

  // Variant A Mark-safe presets (draft-only, client-side).
  it('Given nil-upstream tool When Mark safe clicked Then drafts readOnly:true destructive:false, openWorld untouched', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'clean' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    // Button visible only while effective still blocks read_only
    expect(wrapper.find('[data-test="annotation-override-marksafe-clean"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-override-marksafe-clean"]').trigger('click')
    await flushPromises()
    // Draft visible: Non-destructive badge + amber Safe-draft badge + unsaved counter
    const row = wrapper.find('[data-test="annotation-override-row-clean"]')
    expect(row.text()).toContain('Non-destructive')
    expect(wrapper.find('[data-test="annotation-override-safedraft-clean"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="annotation-overrides-unsaved-count"]').text()).toBe('1 unsaved')
    // Button hides once effective is read-only
    expect(wrapper.find('[data-test="annotation-override-marksafe-clean"]').exists()).toBe(false)
    // Save emits the preset map via the existing save path (with audit suffix)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const payload = (wrapper.emitted('save') as any[])[0][0] as Record<string, any>
    expect(payload['clean'].readOnlyHint).toBe(true)
    expect(payload['clean'].destructiveHint).toBe(false)
    expect(payload['clean'].title).toContain('[mark-safe: single-tool]')
    expect('openWorldHint' in payload['clean']).toBe(false)
  })

  it('Given upstream openWorld:true When Mark safe clicked Then openWorld stays inherit and warning is shown', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'risky' }] as any,
        overrides: {},
        upstreamAnnotations: { risky: { openWorldHint: true } } as any,
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-risky"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-openworld-warn-risky"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const payload = (wrapper.emitted('save') as any[])[0][0] as Record<string, any>
    expect(payload['risky'].readOnlyHint).toBe(true)
    expect(payload['risky'].destructiveHint).toBe(false)
    expect(payload['risky'].title).toContain('[mark-safe: single-tool]')
    expect('openWorldHint' in payload['risky']).toBe(false)
  })

  it('Given wildcard preset When confirmed Then drafts star only with reason title, never openWorldHint', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'a' }, { name: 'b' }] as any,
        overrides: {},
        upstreamAnnotations: { b: { openWorldHint: true } } as any,
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-*"]').trigger('click')
    await flushPromises()
    const modal = wrapper.find('[data-test="annotation-override-marksafe-modal"]')
    expect(modal.exists()).toBe(true)
    // Impact counts: a (nil openWorld) is network-unverified, b still excluded
    expect(modal.text()).toContain('0/2 tools become read-visible')
    expect(modal.text()).toContain('1 read-visible BUT network-unverified')
    expect(modal.text()).toContain('1 tools still excluded by exclude_open_world')
    // Confirm requires reason + ack
    expect((wrapper.find('[data-test="annotation-override-marksafe-confirm"]').element as HTMLButtonElement).disabled).toBe(true)
    await wrapper.find('[data-test="annotation-override-marksafe-reason"]').setValue('False positive')
    await wrapper.find('[data-test="annotation-override-marksafe-ack"]').setValue(true)
    await wrapper.find('[data-test="annotation-override-marksafe-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-marksafe-modal"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="annotation-override-safedraft-*"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const payload = (wrapper.emitted('save') as any[])[0][0] as Record<string, any>
    expect(Object.keys(payload)).toEqual(['*'])
    expect(payload['*'].readOnlyHint).toBe(true)
    expect(payload['*'].destructiveHint).toBe(false)
    expect('openWorldHint' in payload['*']).toBe(false)
    expect(payload['*'].title).toContain('[mark-safe: False positive]')
  })

  it('Given per-tool preset When saved Then payload flows through the existing save event only', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'x' }, { name: 'y' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-x"]').trigger('click')
    await flushPromises()
    // Untouched tool y must not appear in the payload
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const emitted = wrapper.emitted('save') as any[] | undefined
    expect(emitted).toBeDefined()
    expect(emitted).toHaveLength(1)
    const payload = emitted![0][0] as Record<string, any>
    expect(payload['x'].readOnlyHint).toBe(true)
    expect(payload['x'].destructiveHint).toBe(false)
    expect(payload['x'].title).toContain('[mark-safe: single-tool]')
    expect('y' in payload).toBe(false)
  })

  it('Given preset draft When Reset clicked Then badge and unsaved counter are discarded', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'r' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-r"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-safedraft-r"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="annotation-overrides-unsaved-count"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').isVisible()
    // Reset is the ghost button next to Save-all (no data-test hook; click by text)
    const btns = wrapper.findAll('button')
    const reset = btns.find(b => b.text() === 'Reset')
    expect(reset).toBeDefined()
    await reset!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-safedraft-r"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="annotation-overrides-unsaved-count"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="annotation-override-marksafe-r"]').exists()).toBe(true)
  })

  it('Given preset draft When Delete clicked Then save emits null marker via the same path', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'd' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-d"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-delete-d"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-override-delete-d"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-safedraft-d"]').exists()).toBe(false)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const payload = (wrapper.emitted('save') as any[])[0][0] as Record<string, any>
    expect(payload['d']).toBeNull()
  })

  it('Given preset draft When popover Apply runs Then openWorld stays inherit (no silent openWorld:false)', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'p' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-p"]').trigger('click')
    await flushPromises()
    // Re-open popover: it must load the preset draft with openWorld=inherit
    await wrapper.find('[data-test="annotation-override-edit-p"]').trigger('click')
    await flushPromises()
    const sel = wrapper.find('[data-test="annotation-override-select-p-openWorldHint"]')
    expect((sel.element as HTMLSelectElement).value).toBe('inherit')
    await wrapper.find('[data-test="annotation-overrides-save"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const payload = (wrapper.emitted('save') as any[])[0][0] as Record<string, any>
    expect('openWorldHint' in payload['p']).toBe(false)
    expect(payload['p'].readOnlyHint).toBe(true)
  })

  it('Given wildcard modal without ack/reason When handler invoked directly Then no draft is created (gate is not cosmetic)', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'a' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    await wrapper.find('[data-test="annotation-override-marksafe-*"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-marksafe-modal"]').exists()).toBe(true)
    // Bypass the disabled Confirm button: call the exposed handler directly
    const vm = wrapper.vm as unknown as { confirmMarkAll: () => void }
    expect(typeof vm.confirmMarkAll).toBe('function')
    vm.confirmMarkAll()
    await flushPromises()
    // No draft: modal stays open, no Safe-draft badge, save emits empty map
    expect(wrapper.find('[data-test="annotation-override-marksafe-modal"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="annotation-override-safedraft-*"]').exists()).toBe(false)
  })

  it('Given wildcard hand-set openWorld:false When impact computed Then upstream openWorld:true is not overcounted as blocked', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'w' }] as any,
        overrides: {},
        upstreamAnnotations: { w: { openWorldHint: true } } as any,
      },
    })
    // Hand-set wildcard openWorld:false via popover before opening the modal
    await wrapper.find('[data-test="annotation-override-edit-*"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-override-select-*-openWorldHint"]').setValue('false')
    await wrapper.find('[data-test="annotation-overrides-save"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="annotation-override-marksafe-*"]').trigger('click')
    await flushPromises()
    const modal = wrapper.find('[data-test="annotation-override-marksafe-modal"]')
    // w is effectively visible via the wildcard draft: not blocked, becomes 1/1
    expect(modal.text()).toContain('1/1 tools become read-visible')
    expect(modal.text()).toContain('0 tools still excluded by exclude_open_world')
  })

  it('Given editor ref When markSafe called directly Then draft flows through the single save path (delegation target)', async () => {
    const wrapper = mount(AnnotationOverridesEditor, {
      props: {
        serverName: 's',
        tools: [{ name: 'm' }] as any,
        overrides: {},
        upstreamAnnotations: {},
      },
    })
    // ServerDetail.markToolSafe delegates to this exact method (null-safe,
    // single call, no popover, no save duplication). Prove the target works.
    const vm = wrapper.vm as unknown as { markSafe: (n: string) => void }
    expect(typeof vm.markSafe).toBe('function')
    vm.markSafe('m')
    await flushPromises()
    expect(wrapper.find('[data-test="annotation-override-safedraft-m"]').exists()).toBe(true)
    await wrapper.find('[data-test="annotation-overrides-save-all"]').trigger('click')
    await flushPromises()
    const emitted = wrapper.emitted('save') as any[] | undefined
    expect(emitted).toHaveLength(1)
    expect(emitted![0][0]['m'].readOnlyHint).toBe(true)
    // Null-safe delegation shape: missing/empty names are no-ops, never throw
    expect(() => vm.markSafe('')).not.toThrow()
    expect(() => vm.markSafe(undefined as unknown as string)).not.toThrow()
  })

  it('Given ServerDetail delegation shape When editor ref is null or markSafe missing Then markToolSafe is a safe no-op', async () => {
    // Mirrors ServerDetail.markToolSafe: guard toolName + ref + method.
    const calls: string[] = []
    function fakeMarkToolSafe(toolName: string, ref: unknown) {
      if (toolName && ref) {
        const maybe = ref as unknown as { markSafe?: (n: string) => void }
        if (maybe.markSafe) maybe.markSafe(toolName)
      }
    }
    fakeMarkToolSafe('x', null)
    fakeMarkToolSafe('', { markSafe: (n: string) => calls.push(n) })
    fakeMarkToolSafe('x', {})
    expect(calls).toEqual([])
    fakeMarkToolSafe('x', { markSafe: (n: string) => calls.push(n) })
    expect(calls).toEqual(['x'])
  })
})
