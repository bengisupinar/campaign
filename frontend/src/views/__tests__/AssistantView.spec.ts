import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory } from 'vue-router'

import { audience, campaign, click, stubAPI, type Call } from '@/__tests__/helpers'
import type { AssistantTurn } from '@/api'
import App from '@/App.vue'
import { createAppRouter } from '@/router'

const turn = (fields: Partial<AssistantTurn> = {}): AssistantTurn => ({
  conversation_id: 'conv-1',
  reply: 'Done.',
  actions: [],
  ...fields,
})

async function openAssistant(routes: Parameters<typeof stubAPI>[0]) {
  const calls = stubAPI({
    'GET /readyz': { status: 200 },
    'GET /api/campaigns': { status: 200, body: { campaigns: [] } },
    ...routes,
  })
  const router = createAppRouter(createMemoryHistory())
  await router.push('/assistant')
  const wrapper = mount(App, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { wrapper, router, calls }
}

async function write(wrapper: Awaited<ReturnType<typeof openAssistant>>['wrapper'], text: string) {
  await wrapper.find('textarea').setValue(text)
  await wrapper.find('form').trigger('submit')
  await flushPromises()
}

const messagesSent = (calls: Call[]) =>
  calls.filter((c) => c.url === '/api/assistant/messages').map((c) => c.body)

describe('AssistantView', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    document.body.innerHTML = ''
  })

  it('carries on a conversation', async () => {
    let n = 0
    const { wrapper, calls } = await openAssistant({
      'POST /api/assistant/messages': () => ({
        status: 200,
        body: turn({ reply: ++n === 1 ? 'Hangi şehir?' : 'Tamam, İstanbul.' }),
      }),
    })
    await write(wrapper, 'Bir kampanya hazırla')
    await write(wrapper, 'İstanbul')

    const bubbles = wrapper.findAll('.bubble').map((b) => b.find('.bubble__text').text())
    expect(bubbles).toEqual([
      'Bir kampanya hazırla',
      'Hangi şehir?',
      'İstanbul',
      'Tamam, İstanbul.',
    ])
    const sent = messagesSent(calls) as {
      conversation_id: string
      message: string
      time_zone: string
    }[]
    expect(sent.map((b) => b.conversation_id)).toEqual(['', 'conv-1'])
    expect(sent[0]!.time_zone).toBe(Intl.DateTimeFormat().resolvedOptions().timeZone)
    wrapper.unmount()
  })

  it('shows that it is thinking, then the reply', async () => {
    let answer: (v: { status: number; body: unknown }) => void = () => {}
    const pending = new Promise<{ status: number; body: unknown }>((resolve) => (answer = resolve))
    const { wrapper } = await openAssistant({})
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => {
        const reply = await pending
        return new Response(JSON.stringify(reply.body), { status: reply.status })
      }),
    )
    await wrapper.find('textarea').setValue('Hello')
    await wrapper.find('form').trigger('submit')
    expect(wrapper.text()).toContain('Thinking…')

    answer({ status: 200, body: turn({ reply: 'Hi! What shall we send?' }) })
    await flushPromises()
    expect(wrapper.text()).not.toContain('Thinking…')
    expect(wrapper.text()).toContain('Hi! What shall we send?')
    wrapper.unmount()
  })

  it('offers to try again, with the message kept', async () => {
    let fail = true
    const { wrapper } = await openAssistant({
      'POST /api/assistant/messages': () =>
        fail
          ? { status: 503, body: { error: 'assistant_busy' } }
          : { status: 200, body: turn({ reply: 'Here I am.' }) },
    })
    await write(wrapper, 'Hello?')
    expect(wrapper.text()).toContain('The assistant is busy right now.')
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('Hello?')
    expect(wrapper.findAll('.bubble')).toHaveLength(0)

    fail = false
    await click(wrapper, 'Try again')
    expect(wrapper.text()).toContain('Here I am.')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows what an interrupted turn finished, without offering to send it again', async () => {
    const { wrapper } = await openAssistant({
      'POST /api/assistant/messages': {
        status: 200,
        body: turn({
          reply: '',
          notice: 'interrupted',
          actions: [
            { type: 'campaign_created', campaign: campaign({ id: 12, name: 'Spring sale' }) },
          ],
        }),
      },
    })
    await write(wrapper, 'Create a spring sale')
    const reply = wrapper.findAll('.bubble')[1]!
    expect(reply.find('.bubble__text').exists()).toBe(false)
    expect(reply.text()).toContain('The assistant was interrupted before it could reply.')
    expect(reply.find('.action').text()).toContain('Spring sale')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('')
    wrapper.unmount()
  })

  it('links a drafted campaign to the editor, and shows estimates', async () => {
    const draft = campaign({ id: 12, name: 'Basket reminder' })
    const { wrapper } = await openAssistant({
      'POST /api/assistant/messages': {
        status: 200,
        body: turn({
          actions: [
            { type: 'campaign_created', campaign: draft },
            { type: 'audience_estimated', campaign: draft, audience },
          ],
        }),
      },
    })
    await write(wrapper, 'Create it')
    const card = wrapper.findAll('.action')[0]!
    expect(card.text()).toContain('Draft created')
    expect(card.text()).toContain('Basket reminder')
    expect(card.find('a[href="/campaigns/12/edit"]').text()).toBe('Open in editor')
    expect(wrapper.findAll('.action')[1]!.text()).toContain('120')
    wrapper.unmount()
  })

  it('sends a campaign only when the user approves the card', async () => {
    const draft = campaign({ id: 12, name: 'Basket reminder', channels: ['sms'] })
    const request = {
      type: 'dispatch_requested' as const,
      campaign: draft,
      approval: { id: 'ap-1', scheduled_at: null, reach: 538 },
    }
    const { wrapper, calls } = await openAssistant({
      'POST /api/assistant/messages': {
        status: 200,
        body: turn({ reply: 'Approve it below.', actions: [request] }),
      },
      'POST /api/assistant/approvals/ap-1': {
        status: 200,
        body: { status: 'dispatched', campaign: { ...draft, status: 'sending' } },
      },
    })
    await write(wrapper, 'Send it now')
    const card = wrapper.find('.action')
    expect(card.text()).toContain('Send this campaign?')
    expect(card.text()).toContain('538')
    expect(card.text()).toContain('Right after approval')
    expect(calls.filter((c) => c.url.startsWith('/api/assistant/approvals'))).toEqual([])

    await click(wrapper, 'Approve')
    expect(calls.filter((c) => c.url.startsWith('/api/assistant/approvals'))).toEqual([
      { method: 'POST', url: '/api/assistant/approvals/ap-1', body: { decision: 'approve' } },
    ])
    expect(card.text()).toContain('Approved: the campaign is being sent.')
    expect(card.find('a[href="/campaigns/12"]').exists()).toBe(true)
    expect(card.findAll('button')).toHaveLength(0)
    wrapper.unmount()
  })

  it('cancels a dispatch, and shows why an approval failed', async () => {
    const draft = campaign({ id: 12 })
    const requests = ['ap-1', 'ap-2'].map((id) => ({
      type: 'dispatch_requested' as const,
      campaign: draft,
      approval: { id, scheduled_at: '2026-11-20T07:00:00Z', reach: 10 },
    }))
    const { wrapper } = await openAssistant({
      'POST /api/assistant/messages': { status: 200, body: turn({ actions: requests }) },
      'POST /api/assistant/approvals/ap-1': { status: 200, body: { status: 'cancelled' } },
      'POST /api/assistant/approvals/ap-2': { status: 409, body: { error: 'campaign_changed' } },
    })
    await write(wrapper, 'Schedule it')
    const [first, second] = wrapper.findAll('.action')
    expect(first!.text()).toContain('Schedule this campaign?')

    await first!.findAll('button')[1]!.trigger('click') // Cancel
    await flushPromises()
    expect(first!.text()).toContain('Cancelled: nothing was sent.')

    await second!.findAll('button')[0]!.trigger('click') // Approve
    await flushPromises()
    expect(second!.text()).toContain('The campaign changed after this request.')
    wrapper.unmount()
  })

  it('keeps the conversation while other pages are open, until a new one starts', async () => {
    const { wrapper, router } = await openAssistant({
      'POST /api/assistant/messages': { status: 200, body: turn({ reply: 'Remember me.' }) },
    })
    await write(wrapper, 'Hello')
    await router.push('/campaigns')
    await flushPromises()
    await router.push('/assistant')
    await flushPromises()
    expect(wrapper.text()).toContain('Remember me.')

    await click(wrapper, 'New conversation')
    expect(wrapper.findAll('.bubble')).toHaveLength(0)
    expect(wrapper.text()).toContain('Try:')
    wrapper.unmount()
  })
})
