import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import App from '../App.vue'
import { setLocale } from '../i18n'
import { createAppRouter } from '../router'
import { stubAPI } from './helpers'

async function openApp() {
  stubAPI({
    'GET /readyz': { status: 200 },
    'GET /api/campaigns': { status: 200, body: { campaigns: [] } },
  })
  const router = createAppRouter(createMemoryHistory())
  await router.push('/')
  const wrapper = mount(App, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('App', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setLocale('en')
    localStorage.clear()
  })

  it('opens on the campaigns, with the API status in the header', async () => {
    const wrapper = await openApp()
    expect(wrapper.get('.brand').text()).toBe('Campaign Composer')
    expect(wrapper.get('h1').text()).toBe('Campaigns')
    expect(wrapper.get('[role="status"]').text()).toBe('API: ready')
  })

  it('switches the language from the header and remembers it', async () => {
    const wrapper = await openApp()
    await wrapper.get('#language').setValue('tr')

    expect(wrapper.get('h1').text()).toBe('Kampanyalar')
    expect(wrapper.get('[role="status"]').text()).toBe('API: hazır')
    expect(document.documentElement.lang).toBe('tr')
    expect(localStorage.getItem('locale')).toBe('tr')
  })
})
