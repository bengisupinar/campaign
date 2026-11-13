import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import ApiStatus from '../ApiStatus.vue'

type FetchResult = Pick<Response, 'ok' | 'status'>

function stubFetch(impl: () => Promise<FetchResult>) {
  const fetchMock = vi.fn<() => Promise<FetchResult>>(impl)
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('ApiStatus', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it.each([
    { status: 200, label: 'API: ready' },
    { status: 503, label: 'API: not ready' },
    { status: 502, label: 'API: unreachable' },
  ])('shows "$label" when /readyz answers $status', async ({ status, label }) => {
    stubFetch(async () => ({ ok: status >= 200 && status < 300, status }))

    const wrapper = mount(ApiStatus)
    await flushPromises()

    expect(wrapper.text()).toBe(label)
  })

  it('shows unreachable when the request fails', async () => {
    stubFetch(async () => {
      throw new TypeError('Failed to fetch')
    })

    const wrapper = mount(ApiStatus)
    await flushPromises()

    expect(wrapper.text()).toBe('API: unreachable')
  })

  it('queries the readiness endpoint', async () => {
    const fetchMock = stubFetch(async () => ({ ok: true, status: 200 }))

    mount(ApiStatus)
    await flushPromises()

    expect(fetchMock).toHaveBeenCalledWith('/readyz', expect.anything())
  })
})
