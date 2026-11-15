import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { FakeEventSource } from '@/__tests__/helpers'
import type { Snapshot } from '@/api'
import DeliveryMonitor from '@/components/DeliveryMonitor.vue'

const tally = (fields: Partial<Snapshot['messages']>) => ({
  total: 0,
  pending: 0,
  accepted: 0,
  delivered: 0,
  failed: 0,
  skipped: 0,
  ...fields,
})

const sending: Snapshot = {
  status: 'sending',
  scheduled_at: null,
  started_at: '2026-11-14T10:00:00Z',
  finished_at: null,
  recipients: 3,
  messages: tally({ total: 5, pending: 2, accepted: 1, delivered: 1, failed: 1 }),
  by_channel: {
    sms: tally({ total: 2, pending: 1, failed: 1 }),
    email: tally({ total: 3, pending: 1, accepted: 1, delivered: 1 }),
  },
  final: false,
}

const final: Snapshot = {
  ...sending,
  status: 'sent',
  finished_at: '2026-11-14T10:05:00Z',
  messages: tally({ total: 5, delivered: 3, failed: 1, skipped: 1 }),
  by_channel: {
    sms: tally({ total: 2, failed: 1, skipped: 1 }),
    email: tally({ total: 3, delivered: 3 }),
  },
  final: true,
}

// countsOf reads a channel's counts as shown: status name → number.
function countsOf(wrapper: ReturnType<typeof mount>, channel: 'sms' | 'email') {
  const counts: Record<string, string> = {}
  for (const item of wrapper.findAll(`[data-channel="${channel}"] .tally > div`)) {
    counts[item.find('dt').text()] = item.find('dd').text()
  }
  return counts
}

describe('DeliveryMonitor', () => {
  beforeEach(() => {
    FakeEventSource.instances = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('follows the campaign’s stream and shows SMS and email apart', async () => {
    const wrapper = mount(DeliveryMonitor, { props: { campaignId: 7 } })
    const source = FakeEventSource.last()
    expect(source.url).toBe('/api/campaigns/7/delivery/events')

    source.open()
    source.emit(sending)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).toContain('Live')
    expect(wrapper.text()).toContain('Sending messages…')
    expect(countsOf(wrapper, 'sms')).toEqual({
      Delivered: '0',
      Accepted: '0',
      Pending: '1',
      Failed: '1',
      Skipped: '0',
    })
    expect(countsOf(wrapper, 'email')).toEqual({
      Delivered: '1',
      Accepted: '1',
      Pending: '1',
      Failed: '0',
      Skipped: '0',
    })
    expect(wrapper.emitted('snapshot')).toEqual([[sending]])
  })

  it('stops when the progress is final', async () => {
    const wrapper = mount(DeliveryMonitor, { props: { campaignId: 7 } })
    const source = FakeEventSource.last()
    source.open()
    source.emit(final, 'done')
    await wrapper.vm.$nextTick()

    expect(source.readyState).toBe(FakeEventSource.CLOSED)
    expect(wrapper.text()).toContain('Final')
    expect(wrapper.text()).toContain('Complete: every message has its final status.')
    expect(countsOf(wrapper, 'sms')).toMatchObject({ Failed: '1', Skipped: '1' })
  })

  it('reopens a stream the browser gave up on, waiting longer each time', async () => {
    vi.useFakeTimers()
    const wrapper = mount(DeliveryMonitor, { props: { campaignId: 7 } })
    FakeEventSource.last().fail(true)
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Reconnecting…')

    vi.advanceTimersByTime(999)
    expect(FakeEventSource.instances).toHaveLength(1)
    vi.advanceTimersByTime(1)
    expect(FakeEventSource.instances).toHaveLength(2)

    FakeEventSource.last().fail(true)
    vi.advanceTimersByTime(1999)
    expect(FakeEventSource.instances).toHaveLength(2)
    vi.advanceTimersByTime(1)
    expect(FakeEventSource.instances).toHaveLength(3)

    FakeEventSource.last().open()
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Live')
  })

  it('leaves reconnecting to the browser while it retries', () => {
    vi.useFakeTimers()
    mount(DeliveryMonitor, { props: { campaignId: 7 } })
    FakeEventSource.last().fail(false)
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances).toHaveLength(1)
  })

  it('closes the stream and any planned retry when it goes away', () => {
    vi.useFakeTimers()
    const wrapper = mount(DeliveryMonitor, { props: { campaignId: 7 } })
    const first = FakeEventSource.last()
    first.fail(true)
    wrapper.unmount()
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances).toEqual([first])

    mount(DeliveryMonitor, { props: { campaignId: 8 } }).unmount()
    expect(FakeEventSource.last().readyState).toBe(FakeEventSource.CLOSED)
  })
})
