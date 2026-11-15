import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import {
  audience,
  campaign,
  check,
  click,
  FakeEventSource,
  options,
  stubAPI,
  type Call,
} from '@/__tests__/helpers'
import type { Draft } from '@/api'
import App from '@/App.vue'
import { createAppRouter } from '@/router'

// The wizard is used through the whole app, as a marketer would: the edit page, its routes and
// the details page it leads to.
async function openApp(path: string) {
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router }
}

const saved = campaign() // a valid draft: İstanbul, by SMS

// writes lists the requests that change something, leaving out reads and estimates.
const writes = (calls: Call[]) =>
  calls.filter((c) => c.method !== 'GET' && c.url !== '/api/segments/audience')

const readRoutes = {
  'GET /readyz': { status: 200 },
  'GET /api/segments/options': { status: 200, body: options },
  'POST /api/segments/audience': { status: 200, body: audience },
}

function localInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

describe('CampaignWizard', () => {
  let unmount: (() => void) | undefined

  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => {
    unmount?.()
    vi.unstubAllGlobals()
  })

  it('composes a campaign in three steps and sends it once confirmed', async () => {
    const calls = stubAPI({
      ...readRoutes,
      'POST /api/campaigns': (call) => ({ status: 201, body: campaign(call.body as Draft) }),
      'POST /api/campaigns/7/dispatch': { status: 202, body: campaign({ status: 'sending' }) },
      'GET /api/campaigns/7': { status: 200, body: campaign({ status: 'sending' }) },
    })
    const { wrapper, router } = await openApp('/campaigns/new')
    unmount = () => wrapper.unmount()

    await wrapper.find('input[type="text"]').setValue('Spring sale')
    await click(wrapper, '+ Add condition')
    await check(wrapper, 'İstanbul')
    await click(wrapper, 'Next')
    expect(wrapper.find('h2').text()).toBe('Message')

    await check(wrapper, 'SMS')
    await wrapper.find('textarea').setValue('Hi {{first_name}}!')
    await click(wrapper, 'Next')
    expect(wrapper.text()).toContain('City: İstanbul')

    await click(wrapper, 'Send now')
    const dialog = wrapper.find('dialog')
    expect((dialog.element as HTMLDialogElement).open).toBe(true)
    expect(dialog.text()).toContain('“Spring sale” will be sent by SMS right away.')
    expect(writes(calls)).toEqual([])

    await dialog.find('[data-test="confirm"]').trigger('click')
    await flushPromises()
    expect(writes(calls)).toEqual([
      {
        method: 'POST',
        url: '/api/campaigns',
        body: {
          name: 'Spring sale',
          segment: { version: 1, match: 'all', conditions: [{ type: 'city', in: ['İstanbul'] }] },
          content: { sms: { body: 'Hi {{first_name}}!' } },
        },
      },
      { method: 'POST', url: '/api/campaigns/7/dispatch', body: undefined },
    ])
    expect(router.currentRoute.value.path).toBe('/campaigns/7')
    expect(wrapper.text()).toContain('The campaign is being sent.')
    expect(FakeEventSource.last().url).toBe('/api/campaigns/7/delivery/events')
  })

  it('shows a step’s problems instead of moving on', async () => {
    stubAPI(readRoutes)
    const { wrapper } = await openApp('/campaigns/new')
    unmount = () => wrapper.unmount()

    await click(wrapper, '+ Add condition')
    await click(wrapper, 'Next')
    expect(wrapper.find('h2').text()).toBe('Audience')
    expect(wrapper.text()).toContain('Required.')
    expect(wrapper.text()).toContain('Select at least one.')
  })

  it('takes the API’s problems to their field and step', async () => {
    const unknown = { field: 'segment.conditions[0].in[0]', problem: 'unknown', detail: 'Istanbul' }
    const draft = campaign({
      segment: { version: 1, match: 'all', conditions: [{ type: 'city', in: ['Istanbul'] }] },
    })
    const calls = stubAPI({
      ...readRoutes,
      'GET /api/campaigns/7': { status: 200, body: draft },
      'PUT /api/campaigns/7': {
        status: 422,
        body: { error: 'invalid_campaign', problems: [unknown] },
      },
    })
    const { wrapper } = await openApp('/campaigns/7/edit?step=3')
    unmount = () => wrapper.unmount()

    await click(wrapper, 'Save draft')
    expect(writes(calls).map((c) => c.method)).toEqual(['PUT'])
    expect(wrapper.find('h2').text()).toBe('Audience')
    expect(wrapper.find('.condition').text()).toContain('Doesn’t exist: Istanbul.')
  })

  it('sends nothing when the confirmation is cancelled', async () => {
    const calls = stubAPI({ ...readRoutes, 'GET /api/campaigns/7': { status: 200, body: saved } })
    const { wrapper } = await openApp('/campaigns/7/edit?step=3')
    unmount = () => wrapper.unmount()

    await click(wrapper, 'Send now')
    await click(wrapper, 'Cancel')
    expect((wrapper.find('dialog').element as HTMLDialogElement).open).toBe(false)
    expect(writes(calls)).toEqual([])
  })

  it('schedules a campaign at the chosen time, but never in the past', async () => {
    const calls = stubAPI({
      ...readRoutes,
      'GET /api/campaigns/7': { status: 200, body: saved },
      'PUT /api/campaigns/7': { status: 200, body: saved },
      'POST /api/campaigns/7/dispatch': {
        status: 202,
        body: campaign({ status: 'scheduled', scheduled_at: '2030-01-01T00:00:00Z' }),
      },
    })
    const { wrapper } = await openApp('/campaigns/7/edit?step=3')
    unmount = () => wrapper.unmount()

    await check(wrapper, 'Schedule for later')
    const when = wrapper.find('input[type="datetime-local"]')
    await when.setValue('2020-01-01T10:00')
    await click(wrapper, 'Schedule')
    expect(wrapper.text()).toContain('Choose a time in the future, at most a year ahead.')
    expect((wrapper.find('dialog').element as HTMLDialogElement).open).toBe(false)

    const tomorrow = new Date(Date.now() + 86_400_000)
    tomorrow.setSeconds(0, 0)
    await when.setValue(localInput(tomorrow))
    await click(wrapper, 'Schedule')
    await wrapper.find('[data-test="confirm"]').trigger('click')
    await flushPromises()
    expect(writes(calls).map((c) => [c.method, c.url, c.body])).toEqual([
      [
        'PUT',
        '/api/campaigns/7',
        { name: saved.name, segment: saved.segment, content: saved.content },
      ],
      ['POST', '/api/campaigns/7/dispatch', { scheduled_at: tomorrow.toISOString() }],
    ])
  })

  it('asks before leaving with unsaved changes', async () => {
    stubAPI({ ...readRoutes, 'GET /api/campaigns': { status: 200, body: { campaigns: [] } } })
    const { wrapper, router } = await openApp('/campaigns/new')
    unmount = () => wrapper.unmount()

    await wrapper.find('input[type="text"]').setValue('Half written')
    const leave = router.push('/campaigns')
    await flushPromises()
    expect(wrapper.find('dialog').text()).toContain('Leave without saving?')

    await wrapper.find('[data-test="confirm"]').trigger('click')
    await leave
    await flushPromises()
    expect(router.currentRoute.value.path).toBe('/campaigns')
  })

  it('opens every step of a saved draft, but a new one step by step', async () => {
    stubAPI({ ...readRoutes, 'GET /api/campaigns/7': { status: 200, body: saved } })
    const steps = (wrapper: { findAll: (s: string) => { attributes: (a: string) => unknown }[] }) =>
      wrapper.findAll('.steps__button').map((b) => b.attributes('disabled') === undefined)

    const fresh = await openApp('/campaigns/new')
    expect(steps(fresh.wrapper)).toEqual([true, false, false])
    fresh.wrapper.unmount()

    const { wrapper } = await openApp('/campaigns/7/edit')
    unmount = () => wrapper.unmount()
    expect(steps(wrapper)).toEqual([true, true, true])
    await click(wrapper, '3 Review and send')
    expect(wrapper.find('h2').text()).toBe('Review and send')
  })
})
