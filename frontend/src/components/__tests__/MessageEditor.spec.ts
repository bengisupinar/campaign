import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'

import { check, click } from '@/__tests__/helpers'
import type { Content, Problem } from '@/api'
import MessageEditor from '@/components/MessageEditor.vue'
import { setLocale } from '@/i18n'

function mountEditor(content: Content, problems: Problem[] = []) {
  const wrapper = mount(MessageEditor, {
    props: {
      modelValue: content,
      'onUpdate:modelValue': (value: Content) => wrapper.setProps({ modelValue: value }),
      problems,
    },
  })
  return wrapper
}

const contentOf = (wrapper: ReturnType<typeof mountEditor>) =>
  wrapper.props('modelValue') as Content

describe('MessageEditor', () => {
  afterEach(() => setLocale('en'))

  it('turns channels on and off, keeping their text', async () => {
    const wrapper = mountEditor({})
    await check(wrapper, 'SMS')
    await wrapper.find('textarea').setValue('Hello')
    await check(wrapper, 'SMS', false)
    expect(contentOf(wrapper).sms).toBeUndefined()
    expect(wrapper.find('textarea').exists()).toBe(false)

    await check(wrapper, 'SMS')
    expect(contentOf(wrapper).sms).toEqual({ body: 'Hello' })
  })

  it('counts characters as the API does', async () => {
    const wrapper = mountEditor({ sms: { body: '👋' }, email: { subject: '', body: '' } })
    expect(wrapper.find('.counter').text()).toBe('1 / 160')
    await wrapper.find('textarea').setValue('ş'.repeat(161))
    expect(wrapper.find('.counter').text()).toBe('161 / 160')
    expect(wrapper.find('.counter').classes()).toContain('counter--over')
  })

  it('inserts a placeholder at the cursor', async () => {
    const wrapper = mountEditor({ sms: { body: 'Hello !' } })
    const textarea = wrapper.find('textarea').element
    textarea.setSelectionRange(6, 6)
    await click(wrapper, '{{first_name}}')
    expect(contentOf(wrapper).sms).toEqual({ body: 'Hello {{first_name}}!' })
  })

  it('previews messages with sample values in the current language', async () => {
    const wrapper = mountEditor({
      email: { subject: 'For {{first_name}}', body: 'News from {{city}}' },
    })
    expect(wrapper.find('.email-card').text()).toContain('For Jane')
    expect(wrapper.find('.email-card').text()).toContain('News from London')
    setLocale('tr')
    await wrapper.vm.$nextTick()
    expect(wrapper.find('.email-card').text()).toContain('For Ayşe')
  })

  it('shows problems next to their field', () => {
    const wrapper = mountEditor({ email: { subject: 'x'.repeat(81), body: 'Hi {{nickname}}' } }, [
      { field: 'content.email.subject', problem: 'too_long' },
      { field: 'content.email.body', problem: 'unknown_placeholder', detail: 'nickname' },
    ])
    const [subject, body] = wrapper.findAll('.field')
    expect(subject!.text()).toContain('Too long.')
    expect(body!.text()).toContain('Unknown placeholder: nickname.')
  })
})
