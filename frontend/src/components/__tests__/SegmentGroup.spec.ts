import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'

import { check, click, options } from '@/__tests__/helpers'
import type { Condition, Problem, Segment } from '@/api'
import SegmentGroup from '@/components/SegmentGroup.vue'

// mountBuilder edits segment as the wizard does: each new value replaces the old one.
function mountBuilder(
  conditions: Condition[] = [],
  fields: { room?: number; problems?: Problem[] } = {},
) {
  const wrapper = mount(SegmentGroup<Segment>, {
    props: {
      modelValue: { version: 1, match: 'all', conditions } satisfies Segment,
      'onUpdate:modelValue': (value: Segment) => wrapper.setProps({ modelValue: value }),
      path: 'segment',
      depth: 1,
      room: fields.room ?? 20,
      options,
      problems: fields.problems ?? [],
    },
  })
  return wrapper
}

const segmentOf = (wrapper: ReturnType<typeof mountBuilder>) =>
  wrapper.props('modelValue') as Segment

describe('SegmentGroup', () => {
  it('builds the segment without JSON', async () => {
    const wrapper = mountBuilder()
    expect(wrapper.text()).toContain('All customers.')

    await click(wrapper, '+ Add condition')
    await check(wrapper, 'İstanbul')
    await click(wrapper, '+ Add condition')
    const second = wrapper.findAll('.condition')[1]!
    await second.find('select').setValue('purchased')
    const days = second.find('input[type="number"]')
    await days.setValue('30')
    await days.trigger('change')
    await second.find('input[type="checkbox"]').setValue(true) // Exclude
    await click(wrapper, '+ Add group')
    await wrapper.findAll('.group--nested select')[0]!.setValue('any')

    expect(segmentOf(wrapper)).toEqual({
      version: 1,
      match: 'all',
      conditions: [
        { type: 'city', in: ['İstanbul'] },
        { type: 'purchased', within_days: 30, negate: true },
        { type: 'group', match: 'any', conditions: [{ type: 'city', in: [] }] },
      ],
    })
  })

  it('removes conditions and groups', async () => {
    const wrapper = mountBuilder([
      { type: 'basket' },
      { type: 'group', match: 'all', conditions: [{ type: 'signed_up', within_days: 7 }] },
    ])
    await click(wrapper, 'Remove group')
    await click(wrapper, 'Remove')
    expect(segmentOf(wrapper).conditions).toEqual([])
  })

  it('offers no more than the segment language allows', async () => {
    const deepest = { type: 'group', match: 'all', conditions: [{ type: 'basket' }] } as Condition
    const wrapper = mountBuilder([{ type: 'group', match: 'all', conditions: [deepest] }])
    const addGroup = wrapper.findAll('button').filter((b) => b.text() === '+ Add group')
    expect(addGroup.map((b) => b.attributes('disabled') !== undefined)).toEqual([
      true,
      false,
      false,
    ])

    const full = mountBuilder([{ type: 'basket' }], { room: 0 })
    expect(
      full
        .findAll('button')
        .filter((b) => b.text().startsWith('+'))
        .every((b) => b.attributes('disabled') !== undefined),
    ).toBe(true)
    expect(full.text()).toContain('A segment has at most 20 conditions.')
  })

  it('shows each problem with its condition', () => {
    const wrapper = mountBuilder([{ type: 'basket' }, { type: 'city', in: [] }], {
      problems: [{ field: 'segment.conditions[1].in', problem: 'required' }],
    })
    const [first, second] = wrapper.findAll('.condition')
    expect(first!.text()).not.toContain('Select at least one.')
    expect(second!.text()).toContain('Select at least one.')
  })

  it('keeps values the reference data lacks visible, so they can be removed', async () => {
    const wrapper = mountBuilder([{ type: 'city', in: ['Ankara', 'Istanbul'] }])
    expect(wrapper.text()).toContain('Not in the reference data')
    await check(wrapper, 'Istanbul', false)
    expect(segmentOf(wrapper).conditions).toEqual([{ type: 'city', in: ['Ankara'] }])
  })

  it('leaves optional categories out rather than empty', async () => {
    const wrapper = mountBuilder([{ type: 'basket' }])
    await check(wrapper, 'Books')
    expect(segmentOf(wrapper).conditions).toEqual([{ type: 'basket', categories: ['Books'] }])
    await check(wrapper, 'Books', false)
    expect(segmentOf(wrapper).conditions[0]!.categories).toBeUndefined()
  })

  it('takes amounts in lira and stores kuruş', async () => {
    const wrapper = mountBuilder([{ type: 'total_spent', min: 100_000 }])
    const min = wrapper.find('input[type="number"][step="0.01"]')
    expect((min.element as HTMLInputElement).value).toBe('1000')
    await min.setValue('250.5')
    await min.trigger('change')
    expect(segmentOf(wrapper).conditions).toEqual([{ type: 'total_spent', min: 25_050 }])
  })
})
