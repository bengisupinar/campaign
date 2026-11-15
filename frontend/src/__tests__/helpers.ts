// Test doubles for the browser's network APIs (a scripted API behind fetch, and an EventSource
// the test drives), and ways to use a page as a person would: by the texts on it.
import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { vi } from 'vitest'

import type { Campaign } from '@/api'

// click presses the first button whose text is text.
export async function click(wrapper: VueWrapper, text: string) {
  const button = wrapper.findAll('button').find((b) => b.text() === text)
  if (!button) throw new Error(`no button "${text}" in: ${wrapper.text()}`)
  await button.trigger('click')
  await flushPromises()
}

// check ticks or clears the checkbox or radio button labelled text.
export async function check(wrapper: VueWrapper, text: string, checked = true) {
  const label = wrapper.findAll('label').find((l) => l.text() === text)
  if (!label) throw new Error(`no label "${text}" in: ${wrapper.text()}`)
  await label.find('input').setValue(checked)
  await flushPromises()
}

export interface Call {
  method: string
  url: string
  body: unknown
}

interface Reply {
  status: number
  body?: unknown
}

// stubAPI answers fetch from routes keyed "METHOD /path", and records every call. Anything else
// gets the API's 404.
export function stubAPI(routes: Record<string, Reply | ((call: Call) => Reply)>): Call[] {
  const calls: Call[] = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string, init: RequestInit = {}) => {
      const call = {
        method: init.method ?? 'GET',
        url,
        body: typeof init.body === 'string' ? JSON.parse(init.body) : undefined,
      }
      calls.push(call)
      const route = routes[`${call.method} ${url}`]
      const reply =
        route === undefined
          ? { status: 404, body: { error: 'not_found' } }
          : typeof route === 'function'
            ? route(call)
            : route
      return new Response(reply.body === undefined ? null : JSON.stringify(reply.body), {
        status: reply.status,
        headers: { 'Content-Type': 'application/json' },
      })
    }),
  )
  return calls
}

export const options = {
  countries: [
    { code: 'DE', name: 'Germany' },
    { code: 'TR', name: 'Türkiye' },
  ],
  cities: [
    { name: 'Berlin', country: 'DE' },
    { name: 'Ankara', country: 'TR' },
    { name: 'İstanbul', country: 'TR' },
  ],
  categories: ['Books', 'Electronics'],
  languages: ['tr', 'de', 'en'],
}

export const audience = { matched: 120, reachable: 100, by_channel: { email: 80, sms: 60 } }

export function campaign(fields: Partial<Campaign> = {}): Campaign {
  return {
    id: 7,
    status: 'draft',
    channels: ['sms'],
    name: 'Spring sale',
    segment: { version: 1, match: 'all', conditions: [{ type: 'city', in: ['İstanbul'] }] },
    content: { sms: { body: 'Hi {{first_name}}!' } },
    created_at: '2026-11-14T09:00:00Z',
    updated_at: '2026-11-14T09:30:00Z',
    scheduled_at: null,
    started_at: null,
    finished_at: null,
    ...fields,
  }
}

// FakeEventSource stands in for EventSource: a test opens, feeds and breaks each instance.
export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  readyState = FakeEventSource.CONNECTING
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: (() => void) | null = null
  private listeners = new Map<string, ((event: MessageEvent) => void)[]>()

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  addEventListener(type: string, listener: (event: MessageEvent) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  close() {
    this.readyState = FakeEventSource.CLOSED
  }

  open() {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.()
  }

  // emit sends data as an event: unnamed, like a snapshot, or named, like "done".
  emit(data: unknown, type?: string) {
    const event = new MessageEvent(type ?? 'message', { data: JSON.stringify(data) })
    if (type === undefined) this.onmessage?.(event)
    else this.listeners.get(type)?.forEach((listener) => listener(event))
  }

  // fail reports a broken connection: the browser either retries it or gives up on it.
  fail(gaveUp: boolean) {
    this.readyState = gaveUp ? FakeEventSource.CLOSED : FakeEventSource.CONNECTING
    this.onerror?.()
  }

  static last(): FakeEventSource {
    const source = FakeEventSource.instances.at(-1)
    if (!source) throw new Error('no EventSource was opened')
    return source
  }
}
