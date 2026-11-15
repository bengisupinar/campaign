<script setup lang="ts">
// ConditionRow edits one condition: its type, whether it is excluded, and its parameters.
import { computed, useId } from 'vue'

import type { Condition, ConditionType, Problem, SegmentOptions } from '@/api'
import { conditionTypes, limits, newCondition, problemText } from '@/campaign'
import { countryName, languageName, t } from '@/i18n'

const condition = defineModel<Condition>({ required: true })
const props = defineProps<{
  path: string // the condition's field path, e.g. "segment.conditions[0]"
  options?: SegmentOptions
  problems: Problem[]
}>()
const emit = defineEmits<{ remove: [] }>()
const id = useId()

type ListParam = 'in' | 'categories'
type NumberParam = 'within_days' | 'min' | 'max'

interface ChoiceGroup {
  key: string
  label?: string
  choices: { value: string; label: string }[]
  unknown?: boolean
}

const listParam = computed<ListParam | undefined>(() => {
  switch (condition.value.type) {
    case 'country':
    case 'city':
    case 'language':
      return 'in'
    case 'basket':
    case 'purchased':
      return 'categories'
    default:
      return undefined
  }
})
const takesWindow = computed(() =>
  ['signed_up', 'basket', 'purchased', 'order_count', 'total_spent'].includes(condition.value.type),
)
const takesBounds = computed(() => ['order_count', 'total_spent'].includes(condition.value.type))
const inLira = computed(() => condition.value.type === 'total_spent') // stored in kuruş

const listLegend = computed(() => {
  switch (condition.value.type) {
    case 'country':
      return t('segment.countries')
    case 'city':
      return t('segment.cities')
    case 'language':
      return t('segment.languages')
    default:
      return t('segment.categories')
  }
})

const selected = computed(() => (listParam.value ? (condition.value[listParam.value] ?? []) : []))

// The choices come from the reference data; cities are grouped by country. Values it doesn't
// have, as a proposal may contain, are listed apart so they can be removed.
const choiceGroups = computed(() => {
  const o = props.options
  const groups: ChoiceGroup[] = []
  switch (condition.value.type) {
    case 'country':
      groups.push({
        key: 'all',
        choices: (o?.countries ?? []).map((c) => ({ value: c.code, label: countryName(c.code) })),
      })
      break
    case 'city':
      for (const city of o?.cities ?? []) {
        let group = groups.find((g) => g.key === city.country)
        if (!group) {
          group = { key: city.country, label: countryName(city.country), choices: [] }
          groups.push(group)
        }
        group.choices.push({ value: city.name, label: city.name })
      }
      break
    case 'language':
      groups.push({
        key: 'all',
        choices: (o?.languages ?? []).map((l) => ({ value: l, label: languageName(l) })),
      })
      break
    case 'basket':
    case 'purchased':
      groups.push({
        key: 'all',
        choices: (o?.categories ?? []).map((c) => ({ value: c, label: c })),
      })
      break
  }
  const known = new Set(groups.flatMap((g) => g.choices.map((c) => c.value)))
  const unknown = selected.value.filter((v) => !known.has(v))
  if (unknown.length > 0) {
    groups.push({
      key: 'unknown',
      label: t('segment.unknownValues'),
      choices: unknown.map((v) => ({ value: v, label: v })),
      unknown: true,
    })
  }
  return groups
})

const problemsHere = computed(() =>
  props.problems.filter((p) => p.field === props.path || p.field.startsWith(`${props.path}.`)),
)

function problemsOf(param: string): Problem[] {
  const field = `${props.path}.${param}`
  return problemsHere.value.filter((p) => p.field === field || p.field.startsWith(`${field}[`))
}

// Problems not shown next to an input: a missing min and max, a wrong type, or a parameter
// this type doesn't take.
const otherProblems = computed(() => {
  const shown = [
    listParam.value,
    takesWindow.value && 'within_days',
    takesBounds.value && 'min',
    takesBounds.value && 'max',
  ]
    .filter((p): p is string => typeof p === 'string')
    .flatMap((p) => problemsOf(p))
  return problemsHere.value.filter((p) => !shown.includes(p))
})

function set(patch: Partial<Condition>) {
  condition.value = { ...condition.value, ...patch }
}

function changeType(type: ConditionType) {
  condition.value = { ...newCondition(type), negate: condition.value.negate }
}

function toggle(value: string, on: boolean) {
  const param = listParam.value
  if (!param) return
  const next = on ? [...selected.value, value] : selected.value.filter((v) => v !== value)
  const patch: Partial<Condition> = {}
  // "in" is required, so an empty list stays and is reported. Categories are optional: none
  // means any category, and the API reads an empty list as a mistake.
  patch[param] = param === 'categories' && next.length === 0 ? undefined : next
  set(patch)
}

function numberValue(param: NumberParam): number | string {
  const value = condition.value[param]
  if (value === undefined) return ''
  return inLira.value && param !== 'within_days' ? value / 100 : value
}

function setNumber(param: NumberParam, raw: string) {
  let value: number | undefined = raw.trim() === '' ? undefined : Number(raw)
  if (value !== undefined && Number.isNaN(value)) value = undefined
  if (value !== undefined && inLira.value && param !== 'within_days')
    value = Math.round(value * 100)
  const patch: Partial<Condition> = {}
  patch[param] = value
  set(patch)
}
</script>

<template>
  <div class="condition" :class="{ 'condition--invalid': problemsHere.length > 0 }">
    <div class="condition__head">
      <label class="visually-hidden" :for="`${id}-type`">{{ t('segment.type') }}</label>
      <select
        :id="`${id}-type`"
        class="condition__type"
        :value="condition.type"
        @change="changeType(($event.target as HTMLSelectElement).value as ConditionType)"
      >
        <option v-for="type in conditionTypes" :key="type" :value="type">
          {{ t(`condition.${type}`) }}
        </option>
      </select>
      <label class="check">
        <input
          type="checkbox"
          :checked="condition.negate === true"
          @change="set({ negate: ($event.target as HTMLInputElement).checked || undefined })"
        />
        {{ t('segment.exclude') }}
      </label>
      <button
        type="button"
        class="btn btn-ghost btn-sm condition__remove"
        :aria-label="t('segment.removeCondition')"
        @click="emit('remove')"
      >
        {{ t('action.remove') }}
      </button>
    </div>

    <fieldset v-if="listParam" class="choices">
      <legend class="label">{{ listLegend }}</legend>
      <p v-if="!options" class="hint">{{ t('segment.loadingChoices') }}</p>
      <div v-for="g in choiceGroups" :key="g.key" class="choices__group">
        <span
          v-if="g.label"
          class="choices__label"
          :class="{ 'choices__label--unknown': g.unknown }"
        >
          {{ g.label }}
        </span>
        <div class="choices__list">
          <label
            v-for="c in g.choices"
            :key="c.value"
            class="check choice"
            :class="{ 'choice--unknown': g.unknown }"
          >
            <input
              type="checkbox"
              :checked="selected.includes(c.value)"
              @change="toggle(c.value, ($event.target as HTMLInputElement).checked)"
            />
            {{ c.label }}
          </label>
        </div>
      </div>
      <p v-if="listParam === 'categories'" class="hint">{{ t('segment.anyCategory') }}</p>
      <p v-for="p in problemsOf(listParam)" :key="p.field + p.problem" class="problem">
        {{ problemText(p) }}
      </p>
    </fieldset>

    <div v-if="takesWindow || takesBounds" class="condition__numbers">
      <div v-if="takesWindow" class="field">
        <label :for="`${id}-days`" class="label">
          {{ condition.type === 'signed_up' ? t('segment.signedUpWithin') : t('segment.within') }}
        </label>
        <input
          :id="`${id}-days`"
          type="number"
          min="1"
          :max="limits.windowDays"
          step="1"
          inputmode="numeric"
          :value="numberValue('within_days')"
          :placeholder="condition.type === 'signed_up' ? undefined : t('segment.anyTime')"
          :aria-invalid="problemsOf('within_days').length > 0"
          @change="setNumber('within_days', ($event.target as HTMLInputElement).value)"
        />
        <p v-for="p in problemsOf('within_days')" :key="p.problem" class="problem">
          {{ problemText(p) }}
        </p>
      </div>
      <template v-if="takesBounds">
        <div class="field">
          <label :for="`${id}-min`" class="label">{{
            inLira ? t('segment.minSpent') : t('segment.minOrders')
          }}</label>
          <input
            :id="`${id}-min`"
            type="number"
            min="0"
            :step="inLira ? '0.01' : '1'"
            :value="numberValue('min')"
            :aria-invalid="problemsOf('min').length > 0"
            @change="setNumber('min', ($event.target as HTMLInputElement).value)"
          />
          <p v-for="p in problemsOf('min')" :key="p.problem" class="problem">
            {{ problemText(p) }}
          </p>
        </div>
        <div class="field">
          <label :for="`${id}-max`" class="label">{{
            inLira ? t('segment.maxSpent') : t('segment.maxOrders')
          }}</label>
          <input
            :id="`${id}-max`"
            type="number"
            min="0"
            :step="inLira ? '0.01' : '1'"
            :value="numberValue('max')"
            :aria-invalid="problemsOf('max').length > 0"
            @change="setNumber('max', ($event.target as HTMLInputElement).value)"
          />
          <p v-for="p in problemsOf('max')" :key="p.problem" class="problem">
            {{ problemText(p) }}
          </p>
        </div>
      </template>
    </div>

    <p v-for="p in otherProblems" :key="p.field + p.problem" class="problem">
      {{ problemText(p) }}
    </p>
  </div>
</template>

<style scoped>
.condition {
  display: grid;
  gap: 12px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
}

.condition--invalid {
  border-color: #f4b3ad;
}

.condition__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
}

.condition__type {
  width: auto;
  min-width: 220px;
  font-weight: 550;
}

.condition__remove {
  margin-left: auto;
}

.choices {
  display: grid;
  gap: 8px;
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.choices__group {
  display: grid;
  gap: 6px;
}

.choices__label {
  color: var(--text-subtle);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.choices__label--unknown {
  color: var(--danger);
}

.choices__list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.choice {
  padding: 5px 10px;
  border: 1px solid var(--border);
  border-radius: 999px;
  background: var(--surface);
}

.choice:has(input:checked) {
  border-color: var(--accent);
  background: var(--accent-soft);
  color: var(--accent-text);
}

.choice--unknown,
.choice--unknown:has(input:checked) {
  border-color: var(--danger);
  background: var(--danger-soft);
  color: var(--danger);
}

.condition__numbers {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
}
</style>
