// The API's JSON types and one function per endpoint. Every request goes through request(), so
// errors reach the pages in one shape: ApiError.

import { isMessageKey, t } from '@/i18n'

export type Status = 'draft' | 'scheduled' | 'sending' | 'sent' | 'failed'
export type Channel = 'email' | 'sms'
export type Match = 'all' | 'any'
export type ConditionType =
  | 'country'
  | 'city'
  | 'language'
  | 'signed_up'
  | 'basket'
  | 'purchased'
  | 'order_count'
  | 'total_spent'
  | 'group'

// Condition is one test of a customer in the segment DSL (internal/campaign/segment.go). Which
// fields it has depends on its type; the API rejects any other field.
export interface Condition {
  type: ConditionType
  negate?: boolean
  in?: string[]
  within_days?: number
  categories?: string[]
  min?: number // orders for order_count; kuruş for total_spent
  max?: number
  match?: Match
  conditions?: Condition[]
}

export interface Segment {
  version: number
  match: Match
  conditions: Condition[]
}

export interface Content {
  sms?: { body: string }
  email?: { subject: string; body: string }
}

// Draft is what a marketer edits: the body of create and update requests.
export interface Draft {
  name: string
  segment: Segment
  content: Content
}

export interface Campaign extends Draft {
  id: number
  status: Status
  channels: Channel[]
  created_at: string
  updated_at: string
  scheduled_at: string | null
  started_at: string | null
  finished_at: string | null
}

// Problem is one reason a draft is invalid, at a JSON path such as "segment.conditions[0].in".
export interface Problem {
  field: string
  problem: string
  detail?: string
}

export interface Tally {
  total: number
  pending: number
  accepted: number
  delivered: number
  failed: number
  skipped: number
}

// Snapshot is a dispatched campaign's delivery progress.
export interface Snapshot {
  status: Status
  scheduled_at: string | null
  started_at: string | null
  finished_at: string | null
  recipients: number
  messages: Tally
  by_channel: Partial<Record<Channel, Tally>>
  final: boolean
}

export interface Audience {
  matched: number
  reachable: number
  by_channel: Record<Channel, number>
}

// SegmentOptions are the values a segment's conditions can name.
export interface SegmentOptions {
  countries: { code: string; name: string }[]
  cities: { name: string; country: string }[]
  categories: string[]
  languages: string[]
}

// ApiError is a failed request: its HTTP status (0 when the API couldn't be reached), the API's
// error code and, for an invalid draft or segment, its problems.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly problems: Problem[] = [],
  ) {
    super(`API error ${status}: ${code}`)
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
    })
  } catch (err) {
    if (signal?.aborted) throw err // the caller cancelled it
    throw new ApiError(0, 'network')
  }
  if (res.status === 204) return undefined as T
  const data = await res.json().catch(() => undefined)
  if (!res.ok) {
    // A proxy answers without the API's JSON while the API is down.
    const code =
      typeof data?.error === 'string' ? data.error : res.status >= 500 ? 'unavailable' : 'unknown'
    throw new ApiError(res.status, code, Array.isArray(data?.problems) ? data.problems : [])
  }
  return data as T
}

export async function listCampaigns(): Promise<Campaign[]> {
  const body = await request<{ campaigns: Campaign[] }>('GET', '/api/campaigns')
  return body.campaigns
}

export const getCampaign = (id: number) => request<Campaign>('GET', `/api/campaigns/${id}`)

export const createCampaign = (draft: Draft) => request<Campaign>('POST', '/api/campaigns', draft)

export const updateCampaign = (id: number, draft: Draft) =>
  request<Campaign>('PUT', `/api/campaigns/${id}`, draft)

export const deleteCampaign = (id: number) => request<void>('DELETE', `/api/campaigns/${id}`)

// dispatchCampaign sends a saved draft now, or schedules it when scheduledAt (RFC 3339) is given.
export const dispatchCampaign = (id: number, scheduledAt?: string) =>
  request<Campaign>(
    'POST',
    `/api/campaigns/${id}/dispatch`,
    scheduledAt === undefined ? undefined : { scheduled_at: scheduledAt },
  )

export const segmentOptions = () => request<SegmentOptions>('GET', '/api/segments/options')

export const estimateAudience = (segment: Segment, signal?: AbortSignal) =>
  request<Audience>('POST', '/api/segments/audience', { segment }, signal)

// deliveryEventsURL is the server-sent event stream of a campaign's delivery progress.
export const deliveryEventsURL = (id: number) => `/api/campaigns/${id}/delivery/events`

// errorText describes a failed request in the current language.
export function errorText(err: unknown): string {
  if (err instanceof ApiError) {
    const key = `error.${err.code}`
    if (isMessageKey(key)) return t(key)
  }
  return t('error.unknown')
}
