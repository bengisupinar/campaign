import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import App from '../App.vue'

describe('App', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('renders the title and the API status', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, status: 200 })),
    )

    const wrapper = mount(App)
    await flushPromises()

    expect(wrapper.get('h1').text()).toBe('Campaign Composer')
    expect(wrapper.get('[role="status"]').text()).toBe('API: ready')
  })
})
