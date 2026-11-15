<script setup lang="ts">
// CampaignSummary shows a draft read-only: its audience as an indented list, and its messages.
import { computed } from 'vue'

import type { Condition, Draft } from '@/api'
import { countryName, formatMoney, formatNumber, languageName, t } from '@/i18n'

const props = defineProps<{ draft: Draft }>()

interface Line {
  depth: number // 0 for the segment's own match
  text: string
}

const lines = computed(() => {
  const segment = props.draft.segment
  if (segment.conditions.length === 0) return [{ depth: 0, text: t('segment.everyone') }]
  const out: Line[] = [
    { depth: 0, text: segment.match === 'any' ? t('summary.any') : t('summary.all') },
  ]
  const walk = (conditions: Condition[], depth: number) => {
    for (const c of conditions) {
      const text = describe(c)
      out.push({ depth, text: c.negate ? t('summary.excluded', { condition: text }) : text })
      if (c.type === 'group') walk(c.conditions ?? [], depth + 1)
    }
  }
  walk(segment.conditions, 1)
  return out
})

function describe(c: Condition): string {
  const names = (values: string[] | undefined, name: (v: string) => string = (v) => v) =>
    (values ?? []).map(name).join(', ')
  const window =
    c.within_days === undefined ? t('summary.anyTime') : t('summary.within', { n: c.within_days })
  const amount = (n: number) => (c.type === 'total_spent' ? formatMoney(n) : formatNumber(n))
  const bounds = [
    c.min === undefined ? '' : t('summary.atLeast', { n: amount(c.min) }),
    c.max === undefined ? '' : t('summary.atMost', { n: amount(c.max) }),
  ].filter(Boolean)
  switch (c.type) {
    case 'country':
      return `${t('condition.country')}: ${names(c.in, countryName)}`
    case 'city':
      return `${t('condition.city')}: ${names(c.in)}`
    case 'language':
      return `${t('condition.language')}: ${names(c.in, languageName)}`
    case 'signed_up':
      return `${t('condition.signed_up')} · ${window}`
    case 'basket':
    case 'purchased': {
      const categories = c.categories?.length ? names(c.categories) : t('summary.anyCategory')
      return `${t(`condition.${c.type}`)} · ${categories} · ${window}`
    }
    case 'order_count':
    case 'total_spent':
      return [t(`condition.${c.type}`), ...bounds, window].join(' · ')
    case 'group':
      return c.match === 'any' ? t('summary.any') : t('summary.all')
  }
}
</script>

<template>
  <div class="summary">
    <section class="card">
      <h3>{{ t('summary.audience') }}</h3>
      <ul class="summary__segment">
        <li v-for="(line, i) in lines" :key="i" :style="{ paddingLeft: `${line.depth * 20}px` }">
          {{ line.text }}
        </li>
      </ul>
    </section>
    <section class="card">
      <h3>{{ t('summary.messages') }}</h3>
      <p v-if="!draft.content.sms && !draft.content.email" class="hint">
        {{ t('summary.noMessages') }}
      </p>
      <div v-if="draft.content.sms" class="summary__message">
        <span class="chip">{{ t('channel.sms') }}</span>
        <p class="summary__text">{{ draft.content.sms.body }}</p>
      </div>
      <div v-if="draft.content.email" class="summary__message">
        <span class="chip">{{ t('channel.email') }}</span>
        <p class="summary__subject">{{ draft.content.email.subject }}</p>
        <p class="summary__text">{{ draft.content.email.body }}</p>
      </div>
    </section>
  </div>
</template>

<style scoped>
.summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 16px;
}

.summary h3 {
  margin-bottom: 10px;
}

.summary__segment {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.summary__segment li:first-child {
  color: var(--text-muted);
  font-weight: 550;
}

.summary__message {
  display: grid;
  justify-items: start;
  gap: 6px;
}

.summary__message + .summary__message {
  margin-top: 14px;
  padding-top: 14px;
  border-top: 1px solid var(--border);
}

.summary__subject {
  font-weight: 600;
}

.summary__text {
  color: var(--text-muted);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
