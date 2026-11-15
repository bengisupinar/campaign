<script setup lang="ts" generic="G extends Group">
// SegmentGroup edits a group of conditions: the segment itself (depth 1) or a nested group
// condition. Like every editor here, it never changes what it was given: it emits a new value.
import { computed } from 'vue'

import type { Condition, Match, Problem, SegmentOptions } from '@/api'
import { limits, newCondition, problemText, type Group } from '@/campaign'
import ConditionRow from '@/components/ConditionRow.vue'
import { t } from '@/i18n'

const group = defineModel<G>({ required: true })
const props = defineProps<{
  path: string // the group's field path: "segment" for the root
  depth: number // the depth of the group's own conditions: 1 for the root
  room: number // how many more conditions the segment may have
  options?: SegmentOptions
  problems: Problem[]
}>()
const emit = defineEmits<{ remove: [] }>()

const nested = computed(() => props.depth > 1)
const conditions = computed(() => group.value.conditions ?? [])

// The group's own problems: its match, its list of conditions, and itself being too deep.
const ownProblems = computed(() =>
  props.problems.filter((p) =>
    [props.path, `${props.path}.match`, `${props.path}.conditions`].includes(p.field),
  ),
)

function set(patch: Partial<Group>) {
  group.value = { ...group.value, ...patch }
}

function setAt(i: number, c: Condition) {
  set({ conditions: conditions.value.map((old, j) => (j === i ? c : old)) })
}

function removeAt(i: number) {
  set({ conditions: conditions.value.filter((_, j) => j !== i) })
}

function add(c: Condition) {
  set({ conditions: [...conditions.value, c] })
}
</script>

<template>
  <div class="group" :class="{ 'group--nested': nested }">
    <div class="group__header">
      <label class="group__match">
        <span>{{ nested ? t('segment.groupMatch') : t('segment.match') }}</span>
        <select
          :value="group.match"
          @change="set({ match: ($event.target as HTMLSelectElement).value as Match })"
        >
          <option value="all">{{ t('segment.matchAll') }}</option>
          <option value="any">{{ t('segment.matchAny') }}</option>
        </select>
      </label>
      <template v-if="nested">
        <label class="check">
          <input
            type="checkbox"
            :checked="group.negate === true"
            @change="set({ negate: ($event.target as HTMLInputElement).checked || undefined })"
          />
          {{ t('segment.excludeGroup') }}
        </label>
        <button type="button" class="btn btn-ghost btn-sm group__remove" @click="emit('remove')">
          {{ t('segment.removeGroup') }}
        </button>
      </template>
    </div>

    <p v-if="!nested && conditions.length === 0" class="group__empty">
      {{ t('segment.everyone') }}
    </p>

    <ol v-if="conditions.length > 0" class="group__conditions">
      <li v-for="(c, i) in conditions" :key="i">
        <SegmentGroup
          v-if="c.type === 'group'"
          :model-value="c"
          :path="`${path}.conditions[${i}]`"
          :depth="depth + 1"
          :room="room"
          :options="options"
          :problems="problems"
          @update:model-value="setAt(i, $event)"
          @remove="removeAt(i)"
        />
        <ConditionRow
          v-else
          :model-value="c"
          :path="`${path}.conditions[${i}]`"
          :options="options"
          :problems="problems"
          @update:model-value="setAt(i, $event)"
          @remove="removeAt(i)"
        />
      </li>
    </ol>

    <div class="group__actions">
      <button
        type="button"
        class="btn btn-sm"
        :disabled="room < 1"
        @click="add(newCondition('city'))"
      >
        + {{ t('segment.addCondition') }}
      </button>
      <button
        type="button"
        class="btn btn-sm"
        :disabled="room < 2 || depth >= limits.depth"
        :title="depth >= limits.depth ? t('segment.depthLimit') : undefined"
        @click="add(newCondition('group'))"
      >
        + {{ t('segment.addGroup') }}
      </button>
      <span v-if="room < 1" class="hint">{{
        t('segment.countLimit', { n: limits.conditions })
      }}</span>
    </div>
    <p v-for="p in ownProblems" :key="p.field + p.problem" class="problem">{{ problemText(p) }}</p>
  </div>
</template>

<style scoped>
.group {
  display: grid;
  gap: 12px;
}

.group--nested {
  padding: 14px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius);
  background: var(--surface-muted);
}

.group__header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 16px;
}

.group__match {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  font-size: 0.875rem;
  font-weight: 550;
}

.group__match select {
  width: auto;
  min-height: 32px;
  padding: 4px 8px;
}

.group__remove {
  margin-left: auto;
}

.group__empty {
  padding: 14px;
  border: 1px dashed var(--border-strong);
  border-radius: var(--radius);
  color: var(--text-muted);
  text-align: center;
}

.group__conditions {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.group__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
</style>
