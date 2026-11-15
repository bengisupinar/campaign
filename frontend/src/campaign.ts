// Campaign rules as the API applies them (internal/campaign), mirrored so the composer can
// point at a problem before saving. The API stays the authority: its problems have the same
// field paths and codes, so both kinds are shown the same way.

import type {
  Channel,
  Condition,
  ConditionType,
  Content,
  Draft,
  Match,
  Problem,
  Segment,
} from '@/api'
import { isMessageKey, t } from '@/i18n'

export const limits = {
  name: 100,
  sms: 160, // characters as written: one GSM-7 text message
  emailSubject: 80,
  depth: 3, // the segment root's conditions are at depth 1
  conditions: 20, // in the whole segment, groups included
  listValues: 50,
  windowDays: 3650,
  scheduleAheadDays: 365,
} as const

export const placeholders = ['first_name', 'last_name', 'city'] as const

// conditionTypes are the condition types a segment builder offers, besides groups.
export const conditionTypes = [
  'country',
  'city',
  'language',
  'signed_up',
  'basket',
  'purchased',
  'order_count',
  'total_spent',
] as const satisfies readonly ConditionType[]

// Group is what a segment and a group condition share, as the segment builder edits them.
export interface Group {
  match?: Match
  negate?: boolean
  conditions?: Condition[]
}

export function emptyDraft(): Draft {
  return { name: '', segment: { version: 1, match: 'all', conditions: [] }, content: {} }
}

// newCondition returns a condition of the given type, with valid defaults where the type has
// some. List conditions start empty: the marketer has to choose.
export function newCondition(type: ConditionType): Condition {
  switch (type) {
    case 'country':
    case 'city':
    case 'language':
      return { type, in: [] }
    case 'signed_up':
      return { type, within_days: 30 }
    case 'basket':
    case 'purchased':
      return { type }
    case 'order_count':
      return { type, min: 1 }
    case 'total_spent':
      return { type, min: 100_000 } // 1,000 TRY
    case 'group':
      return { type, match: 'all', conditions: [newCondition('city')] }
  }
}

// countConditions counts a segment's conditions, groups included, as the API limits them.
export function countConditions(conditions: Condition[] = []): number {
  return conditions.reduce((n, c) => n + 1 + countConditions(c.conditions), 0)
}

// channelsOf lists the channels content uses, email first as the API does.
export function channelsOf(content: Content): Channel[] {
  const channels: Channel[] = []
  if (content.email) channels.push('email')
  if (content.sms) channels.push('sms')
  return channels
}

// length counts characters as the API does: Unicode code points, not UTF-16 units.
export function length(text: string): number {
  return [...text].length
}

type Add = (field: string, problem: string, detail?: string) => void

// validateDraft lists the problems the API would report for d, except for names that don't
// exist in the reference data, which only the API can check.
export function validateDraft(d: Draft): Problem[] {
  const problems: Problem[] = []
  const add: Add = (field, problem, detail) =>
    problems.push(detail === undefined ? { field, problem } : { field, problem, detail })

  const name = d.name.trim()
  if (name === '') add('name', 'required')
  else if (length(name) > limits.name) add('name', 'too_long')
  validateSegment(d.segment, add)
  validateContent(d.content, add)
  return problems
}

function validateSegment(s: Segment, add: Add) {
  if (s.match !== 'all' && s.match !== 'any') add('segment.match', 'required')
  validateConditions('segment.conditions', s.conditions, 1, add)
  if (countConditions(s.conditions) > limits.conditions) add('segment.conditions', 'too_many')
}

function validateConditions(path: string, conditions: Condition[], depth: number, add: Add) {
  conditions.forEach((c, i) => {
    const at = `${path}[${i}]`
    switch (c.type) {
      case 'country':
        validateList(`${at}.in`, c.in, true, (v) => /^[A-Z]{2}$/.test(v), add)
        break
      case 'city':
        validateList(`${at}.in`, c.in, true, validName, add)
        break
      case 'language':
        validateList(`${at}.in`, c.in, true, (v) => ['tr', 'de', 'en'].includes(v), add)
        break
      case 'signed_up':
        validateWindow(at, c.within_days, true, add)
        break
      case 'basket':
      case 'purchased':
        validateWindow(at, c.within_days, false, add)
        validateList(`${at}.categories`, c.categories, false, validName, add)
        break
      case 'order_count':
      case 'total_spent':
        validateWindow(at, c.within_days, false, add)
        validateBounds(at, c.min, c.max, add)
        break
      case 'group':
        if (depth >= limits.depth) {
          add(at, 'too_deep')
          break
        }
        if (c.match !== 'all' && c.match !== 'any') add(`${at}.match`, 'required')
        if (!c.conditions?.length) add(`${at}.conditions`, 'required')
        else validateConditions(`${at}.conditions`, c.conditions, depth + 1, add)
        break
      default:
        add(`${at}.type`, c.type ? 'invalid' : 'required')
    }
  })
}

// validateList checks a list parameter: present when required, 1 to 50 values, each valid.
function validateList(
  path: string,
  values: string[] | undefined,
  required: boolean,
  valid: (v: string) => boolean,
  add: Add,
) {
  if (values === undefined) {
    if (required) add(path, 'required')
    return
  }
  if (values.length === 0) {
    add(path, 'required')
    return
  }
  if (values.length > limits.listValues) add(path, 'too_many')
  values.forEach((v, i) => {
    if (!valid(v)) add(`${path}[${i}]`, 'invalid')
  })
}

function validName(v: string): boolean {
  return v.trim() !== '' && v.trim() === v && length(v) <= 100
}

function validateWindow(path: string, days: number | undefined, required: boolean, add: Add) {
  if (days === undefined) {
    if (required) add(`${path}.within_days`, 'required')
  } else if (!Number.isInteger(days) || days < 1 || days > limits.windowDays) {
    add(`${path}.within_days`, 'invalid')
  }
}

function validateBounds(path: string, min: number | undefined, max: number | undefined, add: Add) {
  if (min === undefined && max === undefined) {
    add(path, 'required', 'min or max')
    return
  }
  const minValid = min === undefined || (Number.isInteger(min) && min >= 0)
  const maxValid = max === undefined || (Number.isInteger(max) && max >= 0)
  if (!minValid) add(`${path}.min`, 'invalid')
  if (!maxValid) add(`${path}.max`, 'invalid')
  if (min !== undefined && max !== undefined && minValid && maxValid && min > max)
    add(`${path}.max`, 'invalid')
}

function validateContent(c: Content, add: Add) {
  if (!c.sms && !c.email) {
    add('content', 'required')
    return
  }
  if (c.sms) validateText('content.sms.body', c.sms.body, limits.sms, add)
  if (c.email) {
    validateText('content.email.subject', c.email.subject, limits.emailSubject, add)
    validateText('content.email.body', c.email.body, 0, add)
  }
}

function validateText(field: string, text: string, limit: number, add: Add) {
  if (text.trim() === '') {
    add(field, 'required')
    return
  }
  if (limit > 0 && length(text) > limit) add(field, 'too_long')
  let malformed = false
  for (let rest = text; ;) {
    const open = rest.indexOf('{{')
    const close = rest.indexOf('}}')
    if (open < 0 && close < 0) break
    if (open < 0 || (close >= 0 && close < open)) {
      malformed = true // "}}" on its own
      rest = rest.slice(close + 2)
      continue
    }
    const end = rest.indexOf('}}', open + 2)
    if (end < 0) {
      malformed = true // nothing closes this "{{"
      break
    }
    const name = rest.slice(open + 2, end).trim()
    if (name === '') malformed = true
    else if (!isPlaceholder(name)) add(field, 'unknown_placeholder', truncate(name, 40))
    rest = rest.slice(end + 2)
  }
  if (malformed) add(field, 'malformed_placeholder')
}

// truncate shortens a problem's detail as the API does.
function truncate(s: string, max: number): string {
  const chars = [...s]
  return chars.length > max ? chars.slice(0, max).join('') + '…' : s
}

function isPlaceholder(name: string): name is (typeof placeholders)[number] {
  return (placeholders as readonly string[]).includes(name)
}

// validateSchedule checks a time to schedule a campaign at, as a datetime-local input's value:
// in the future and at most a year ahead.
export function validateSchedule(local: string, now: Date): Problem[] {
  if (local === '') return [{ field: 'scheduled_at', problem: 'required' }]
  const at = new Date(local)
  const ahead = at.getTime() - now.getTime()
  if (Number.isNaN(ahead) || ahead <= 0 || ahead > limits.scheduleAheadDays * 24 * 3600 * 1000) {
    return [{ field: 'scheduled_at', problem: 'invalid' }]
  }
  return []
}

// fill replaces each placeholder in text with its value, as delivery does for each recipient
// (campaign.Fill). Braces that aren't a placeholder are kept as written.
export function fill(text: string, values: Record<(typeof placeholders)[number], string>): string {
  let out = ''
  for (;;) {
    const open = text.indexOf('{{')
    const end = open < 0 ? -1 : text.indexOf('}}', open + 2)
    if (end < 0) break
    const name = text.slice(open + 2, end).trim()
    out += text.slice(0, open) + (isPlaceholder(name) ? values[name] : text.slice(open, end + 2))
    text = text.slice(end + 2)
  }
  return out + text
}

// stepOf tells which composer step shows a field: 1 audience, 2 message, 3 review.
export function stepOf(field: string): 1 | 2 | 3 {
  if (field.startsWith('content')) return 2
  if (field === 'scheduled_at') return 3
  return 1
}

// problemText describes a problem in the current language.
export function problemText(p: Problem): string {
  if (p.problem === 'required') {
    if (p.field === 'content') return t('problem.requiredChannel')
    if (p.field.endsWith('.in')) return t('problem.requiredChoice')
    if (p.detail === 'min or max') return t('problem.requiredBound')
  }
  const key = `problem.${p.problem}`
  return isMessageKey(key) ? t(key, { detail: p.detail ?? '' }) : t('problem.other')
}
