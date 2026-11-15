import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { campaign, click, stubAPI } from '@/__tests__/helpers'
import { createAppRouter } from '@/router'
import CampaignsView from '@/views/CampaignsView.vue'

async function openList() {
  const router = createAppRouter(createMemoryHistory())
  await router.push('/campaigns')
  const wrapper = mount(CampaignsView, { global: { plugins: [router] }, attachTo: document.body })
  return wrapper
}

describe('CampaignsView', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('lists campaigns with their status, channels and actions', async () => {
    stubAPI({
      'GET /api/campaigns': {
        status: 200,
        body: {
          campaigns: [
            campaign({
              id: 2,
              name: 'Winter sale',
              status: 'sent',
              channels: ['email', 'sms'],
              finished_at: '2026-11-14T10:00:00Z',
            }),
            campaign({ id: 1, name: 'Spring sale' }),
          ],
        },
      },
    })
    const wrapper = await openList()
    expect(wrapper.text()).toContain('Loading campaigns…')
    await flushPromises()

    const [sent, draft] = wrapper.findAll('tbody tr')
    expect(sent!.find('.badge').text()).toBe('Sent')
    expect(sent!.findAll('.chip').map((c) => c.text())).toEqual(['Email', 'SMS'])
    expect(sent!.text()).toContain('View')
    expect(draft!.find('.badge').text()).toBe('Draft')
    expect(draft!.text()).toContain('Edit')
    expect(draft!.text()).toContain('Delete')
    wrapper.unmount()
  })

  it('invites to create the first campaign', async () => {
    stubAPI({ 'GET /api/campaigns': { status: 200, body: { campaigns: [] } } })
    const wrapper = await openList()
    await flushPromises()
    expect(wrapper.text()).toContain('No campaigns yet')
    expect(wrapper.findAll('a[href="/campaigns/new"]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('offers to try again after an error', async () => {
    let fail = true
    stubAPI({
      'GET /api/campaigns': () =>
        fail
          ? { status: 500, body: { error: 'internal' } }
          : { status: 200, body: { campaigns: [campaign()] } },
    })
    const wrapper = await openList()
    await flushPromises()
    expect(wrapper.text()).toContain('Something went wrong on the server. Try again.')

    fail = false
    await click(wrapper, 'Try again')
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    wrapper.unmount()
  })

  it('deletes a draft only once confirmed', async () => {
    const calls = stubAPI({
      'GET /api/campaigns': { status: 200, body: { campaigns: [campaign()] } },
      'DELETE /api/campaigns/7': { status: 204 },
    })
    const wrapper = await openList()
    await flushPromises()

    await click(wrapper, 'Delete')
    expect(wrapper.find('dialog').text()).toContain('“Spring sale” will be deleted.')
    await click(wrapper, 'Cancel')
    expect(calls.filter((c) => c.method === 'DELETE')).toEqual([])

    await click(wrapper, 'Delete')
    await wrapper.find('[data-test="confirm"]').trigger('click')
    await flushPromises()
    expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.url)).toEqual([
      '/api/campaigns/7',
    ])
    expect(wrapper.text()).toContain('No campaigns yet')
    wrapper.unmount()
  })
})
