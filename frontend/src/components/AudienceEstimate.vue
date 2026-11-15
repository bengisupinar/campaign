<script setup lang="ts">
// AudienceEstimate shows how many customers a segment matches and can reach, refreshed shortly
// after the segment last changed.
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import {
  ApiError,
  errorText,
  estimateAudience,
  type Audience,
  type Channel,
  type Problem,
  type Segment,
} from '@/api'
import { formatNumber, t } from '@/i18n'

const props = defineProps<{
  segment: Segment
  channels?: Channel[] // the campaign's channels, to show its reach
  paused?: boolean // true while the segment has problems the composer already shows
}>()
const emit = defineEmits<{
  problems: [Problem[]] // what the API found wrong with the segment, such as unknown names
  estimate: [Audience | undefined]
}>()

const delay = 400 // milliseconds after the last change

const audience = ref<Audience>()
const loading = ref(false)
const error = ref('')
const invalid = ref(false)
let timer: ReturnType<typeof setTimeout> | undefined
let controller: AbortController | undefined

const reach = computed(() => {
  const a = audience.value
  const channels = props.channels ?? []
  if (!a || channels.length === 0) return undefined
  return channels.length > 1 ? a.reachable : a.by_channel[channels[0]!]
})

watch(() => [props.segment, props.paused], schedule, { deep: true, immediate: true })
onBeforeUnmount(cancel)

function cancel() {
  clearTimeout(timer)
  controller?.abort()
}

function schedule() {
  cancel()
  if (props.paused) {
    loading.value = false
    emit('problems', [])
    return
  }
  loading.value = true
  timer = setTimeout(load, delay)
}

async function load() {
  const c = new AbortController()
  controller = c
  let result: Audience | undefined
  let failure: unknown
  try {
    result = await estimateAudience(props.segment, c.signal)
  } catch (err) {
    failure = err
  }
  if (c.signal.aborted) return // a newer estimate replaced this one
  audience.value = result
  invalid.value = failure instanceof ApiError && failure.status === 422
  error.value = failure === undefined || invalid.value ? '' : errorText(failure)
  loading.value = false
  emit('problems', failure instanceof ApiError ? failure.problems : [])
  emit('estimate', result)
}
</script>

<template>
  <section class="estimate card" aria-live="polite" :aria-busy="loading">
    <h3>{{ t('estimate.title') }}</h3>
    <p v-if="paused" class="hint">{{ t('estimate.paused') }}</p>
    <p v-else-if="error" class="problem">{{ error }}</p>
    <p v-else-if="invalid" class="hint">{{ t('estimate.invalid') }}</p>
    <template v-else-if="audience">
      <p class="estimate__matched">
        <span class="hint">{{ t('estimate.matched') }}</span>
        <strong>{{ formatNumber(audience.matched) }}</strong>
      </p>
      <dl class="estimate__channels">
        <div>
          <dt>{{ t('estimate.byEmail') }}</dt>
          <dd>{{ formatNumber(audience.by_channel.email) }}</dd>
        </div>
        <div>
          <dt>{{ t('estimate.bySMS') }}</dt>
          <dd>{{ formatNumber(audience.by_channel.sms) }}</dd>
        </div>
      </dl>
      <p v-if="reach !== undefined" class="estimate__reach">
        {{ t('estimate.reach', { n: formatNumber(reach) }) }}
      </p>
      <p class="hint">{{ loading ? t('estimate.updating') : t('estimate.note') }}</p>
    </template>
    <p v-else class="hint">{{ t('estimate.loading') }}</p>
  </section>
</template>

<style scoped>
.estimate {
  display: grid;
  gap: 10px;
  align-content: start;
}

.estimate__matched {
  display: grid;
}

.estimate__matched strong {
  font-size: 1.75rem;
  font-weight: 700;
  letter-spacing: -0.02em;
}

.estimate__channels {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
  margin: 0;
}

.estimate__channels div {
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
}

.estimate__channels dt {
  color: var(--text-subtle);
  font-size: 0.75rem;
}

.estimate__channels dd {
  margin: 0;
  font-size: 1.05rem;
  font-weight: 650;
}

.estimate__reach {
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--accent-soft);
  color: var(--accent-text);
  font-weight: 550;
}
</style>
