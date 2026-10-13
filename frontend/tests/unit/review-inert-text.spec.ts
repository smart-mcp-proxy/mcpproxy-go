import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ToolDefinitionText from '@/components/ToolDefinitionText.vue'

describe('ToolDefinitionText (T087)', () => {
  it('keeps upstream definitions inert and truncates long text until expanded', async () => {
    const wrapper = mount(ToolDefinitionText, { props: { text: `<img src=x onerror=alert(1)>${'x'.repeat(1300)}` } })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('<img src=x')
    expect(wrapper.text()).toContain('Show all')
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).toContain('Show less')
  })
})
