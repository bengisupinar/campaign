import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, createCampaign, dispatchCampaign, errorText, listCampaigns } from '@/api'
import { setLocale } from '@/i18n'

import { campaign, stubAPI } from './helpers'

describe('api', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    setLocale('en')
  })

  it('returns what the API answered', async () => {
    stubAPI({ 'GET /api/campaigns': { status: 200, body: { campaigns: [campaign()] } } })
    expect(await listCampaigns()).toEqual([campaign()])
  })

  it('sends drafts as JSON', async () => {
    const calls = stubAPI({ 'POST /api/campaigns': { status: 201, body: campaign() } })
    const { name, segment, content } = campaign()
    await createCampaign({ name, segment, content })
    expect(calls).toEqual([
      { method: 'POST', url: '/api/campaigns', body: { name, segment, content } },
    ])
  })

  it('dispatches now without a body, or at a time', async () => {
    const calls = stubAPI({ 'POST /api/campaigns/7/dispatch': { status: 202, body: campaign() } })
    await dispatchCampaign(7)
    await dispatchCampaign(7, '2026-11-20T09:00:00.000Z')
    expect(calls.map((c) => c.body)).toEqual([
      undefined,
      { scheduled_at: '2026-11-20T09:00:00.000Z' },
    ])
  })

  it('turns an invalid draft into its problems', async () => {
    const problems = [{ field: 'name', problem: 'required' }]
    stubAPI({
      'POST /api/campaigns': { status: 422, body: { error: 'invalid_campaign', problems } },
    })
    await expect(createCampaign(campaign())).rejects.toEqual(
      new ApiError(422, 'invalid_campaign', problems),
    )
  })

  it.each([
    [
      'the API’s error code',
      () => new Response('{"error":"already_dispatched"}', { status: 409 }),
      409,
      'already_dispatched',
    ],
    [
      'a proxy’s error page',
      () => new Response('<html>Bad Gateway</html>', { status: 502 }),
      502,
      'unavailable',
    ],
    ['an unreachable server', () => Promise.reject(new TypeError('Failed to fetch')), 0, 'network'],
  ])('reports %s', async (_, answer, status, code) => {
    vi.stubGlobal('fetch', vi.fn(answer))
    const err = await dispatchCampaign(7).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status, code })
  })

  it('describes errors in the current language', () => {
    expect(errorText(new ApiError(409, 'already_dispatched'))).toBe(
      'This campaign has already been dispatched.',
    )
    setLocale('tr')
    expect(errorText(new ApiError(0, 'network'))).toBe(
      'Sunucuya ulaşılamadı. Bağlantınızı kontrol edip tekrar deneyin.',
    )
    expect(errorText(new Error('boom'))).toBe('Bir sorun oluştu. Tekrar deneyin.')
  })
})
