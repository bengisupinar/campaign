import { beforeEach, describe, expect, it } from 'vitest'

import type { Condition, Draft } from '@/api'
import { countConditions, fill, problemText, validateDraft, validateSchedule } from '@/campaign'
import { setLocale } from '@/i18n'

function draft(fields: Partial<Draft> = {}): Draft {
  return {
    name: 'Spring sale',
    segment: { version: 1, match: 'all', conditions: [] },
    content: { sms: { body: 'Hi {{first_name}}' } },
    ...fields,
  }
}

const withConditions = (...conditions: Condition[]) =>
  draft({ segment: { version: 1, match: 'all', conditions } })

const group = (...conditions: Condition[]): Condition => ({
  type: 'group',
  match: 'all',
  conditions,
})

// The rules mirror internal/campaign: each case expects the problem the API reports.
describe('validateDraft', () => {
  it('accepts a complete draft', () => {
    expect(validateDraft(draft())).toEqual([])
  })

  it.each<[string, Draft, object[]]>([
    ['a blank name', draft({ name: '  ' }), [{ field: 'name', problem: 'required' }]],
    [
      'a name over 100 characters',
      draft({ name: 'x'.repeat(101) }),
      [{ field: 'name', problem: 'too_long' }],
    ],
    ['no channel', draft({ content: {} }), [{ field: 'content', problem: 'required' }]],
    [
      'an SMS over 160 characters',
      draft({ content: { sms: { body: 'ş'.repeat(161) } } }),
      [{ field: 'content.sms.body', problem: 'too_long' }],
    ],
    [
      'an empty email',
      draft({ content: { email: { subject: '', body: ' ' } } }),
      [
        { field: 'content.email.subject', problem: 'required' },
        { field: 'content.email.body', problem: 'required' },
      ],
    ],
    [
      'an unknown placeholder',
      draft({ content: { sms: { body: 'Hi {{ nickname }}' } } }),
      [{ field: 'content.sms.body', problem: 'unknown_placeholder', detail: 'nickname' }],
    ],
    [
      'braces that are no placeholder',
      draft({ content: { sms: { body: 'Hi }} {{first_name' } } }),
      [{ field: 'content.sms.body', problem: 'malformed_placeholder' }],
    ],
    [
      'an empty city list',
      withConditions({ type: 'city', in: [] }),
      [{ field: 'segment.conditions[0].in', problem: 'required' }],
    ],
    [
      'a country that is no code',
      withConditions({ type: 'country', in: ['TR', 'tr'] }),
      [{ field: 'segment.conditions[0].in[1]', problem: 'invalid' }],
    ],
    [
      'a sign-up condition without days',
      withConditions({ type: 'signed_up' }),
      [{ field: 'segment.conditions[0].within_days', problem: 'required' }],
    ],
    [
      'a window out of range',
      withConditions({ type: 'purchased', within_days: 3651 }),
      [{ field: 'segment.conditions[0].within_days', problem: 'invalid' }],
    ],
    [
      'an empty category list',
      withConditions({ type: 'basket', categories: [] }),
      [{ field: 'segment.conditions[0].categories', problem: 'required' }],
    ],
    [
      'no bounds',
      withConditions({ type: 'order_count' }),
      [{ field: 'segment.conditions[0]', problem: 'required', detail: 'min or max' }],
    ],
    [
      'min above max',
      withConditions({ type: 'total_spent', min: 50_000, max: 10_000 }),
      [{ field: 'segment.conditions[0].max', problem: 'invalid' }],
    ],
    [
      'an empty group',
      withConditions({ type: 'group', match: 'any', conditions: [] }),
      [{ field: 'segment.conditions[0].conditions', problem: 'required' }],
    ],
    [
      'a group three levels deep',
      withConditions(group(group(group({ type: 'basket' })))),
      [{ field: 'segment.conditions[0].conditions[0].conditions[0]', problem: 'too_deep' }],
    ],
    [
      'more than 20 conditions',
      withConditions(...Array.from({ length: 21 }, (): Condition => ({ type: 'basket' }))),
      [{ field: 'segment.conditions', problem: 'too_many' }],
    ],
  ])('reports %s', (_, d, want) => {
    expect(validateDraft(d)).toEqual(want)
  })
})

describe('countConditions', () => {
  it('counts groups and what they hold', () => {
    expect(
      countConditions([{ type: 'basket' }, group({ type: 'basket' }, group({ type: 'basket' }))]),
    ).toBe(5)
  })
})

describe('fill', () => {
  const values = { first_name: 'Ayşe', last_name: 'Yılmaz', city: 'İstanbul' }

  it.each([
    ['Hi {{first_name}} {{ last_name }}!', 'Hi Ayşe Yılmaz!'],
    ['From {{city}}: {{nickname}} stays', 'From İstanbul: {{nickname}} stays'],
    ['Unclosed {{first_name', 'Unclosed {{first_name'],
  ])('fills %j', (text, want) => {
    expect(fill(text, values)).toBe(want)
  })
})

describe('validateSchedule', () => {
  const now = new Date('2026-11-14T12:00:00') // local time, like a datetime-local value

  it.each([
    ['', 'required'],
    ['2026-11-14T11:59', 'invalid'],
    ['2026-11-14T12:30', undefined],
    ['2027-11-15T12:00', 'invalid'],
  ])('checks %j', (local, problem) => {
    expect(validateSchedule(local, now)).toEqual(
      problem ? [{ field: 'scheduled_at', problem }] : [],
    )
  })
})

describe('problemText', () => {
  beforeEach(() => setLocale('en'))

  it.each([
    [{ field: 'content', problem: 'required' }, 'Turn on at least one channel.'],
    [{ field: 'segment.conditions[0].in', problem: 'required' }, 'Select at least one.'],
    [
      { field: 'segment.conditions[1].in[0]', problem: 'unknown', detail: 'Istanbul' },
      'Doesn’t exist: Istanbul.',
    ],
    [{ field: 'name', problem: 'not_a_code' }, 'Not valid.'],
  ])('describes %j', (p, want) => {
    expect(problemText(p)).toBe(want)
  })
})
