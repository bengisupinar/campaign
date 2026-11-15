<script setup lang="ts">
// DeliveryMonitor follows a dispatched campaign's delivery live, over server-sent events, until
// its progress is final: then the stream sends "done" and nothing can change any more.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { deliveryEventsURL, type Snapshot, type Tally } from '@/api'
import { formatDateTime, formatNumber, t } from '@/i18n'

const props = defineProps<{ campaignId: number }>()
const emit = defineEmits<{ snapshot: [Snapshot] }>()

type Connection = 'connecting' | 'live' | 'reconnecting' | 'final'

// How long to wait before reopening a stream the browser gave up on, doubling each time.
const firstRetry = 1000
const maxRetry = 15_000

// Message statuses, in the order they are shown.
const statuses = ['delivered', 'accepted', 'pending', 'failed', 'skipped'] as const

const snapshot = ref<Snapshot>()
const connection = ref<Connection>('connecting')
let source: EventSource | undefined
let retry: ReturnType<typeof setTimeout> | undefined
let wait = firstRetry

onMounted(connect)
onBeforeUnmount(stop)

function connect() {
  const s = new EventSource(deliveryEventsURL(props.campaignId))
  source = s
  s.onopen = () => {
    connection.value = 'live'
    wait = firstRetry
  }
  s.onmessage = (event) => update(event.data)
  s.addEventListener('done', (event) => {
    update((event as MessageEvent<string>).data)
    stop()
    connection.value = 'final'
  })
  s.onerror = () => {
    if (source !== s) return // already replaced or stopped
    connection.value = 'reconnecting'
    // While the source is CONNECTING, the browser retries by itself. A CLOSED one, e.g. after a
    // proxy answered 502 while the API restarted, is ours to reopen.
    if (s.readyState === EventSource.CLOSED) {
      s.close()
      retry = setTimeout(connect, wait)
      wait = Math.min(wait * 2, maxRetry)
    }
  }
}

function stop() {
  clearTimeout(retry)
  source?.close()
  source = undefined
}

function update(data: string) {
  try {
    snapshot.value = JSON.parse(data) as Snapshot
  } catch {
    return // not a snapshot
  }
  emit('snapshot', snapshot.value)
}

const headline = computed(() => {
  const s = snapshot.value
  if (!s) return t('monitor.loading')
  switch (s.status) {
    case 'scheduled':
      return t('monitor.scheduled', { time: s.scheduled_at ? formatDateTime(s.scheduled_at) : '' })
    case 'sending':
      return t('monitor.sending')
    case 'sent':
      return s.final ? t('monitor.completed') : t('monitor.awaitingReceipts')
    case 'failed':
      return t('monitor.failed')
    default:
      return t('monitor.notDispatched')
  }
})

const channels = computed(() =>
  (['sms', 'email'] as const).filter((ch) => snapshot.value?.by_channel[ch]),
)

function percent(n: number, total: number): number {
  return total === 0 ? 0 : Math.round((n / total) * 100)
}

// done counts the messages that reached an outcome: all but the pending ones.
const done = (tally: Tally) => tally.total - tally.pending
</script>

<template>
  <section class="card monitor">
    <div class="monitor__head">
      <div>
        <h2>{{ t('monitor.title') }}</h2>
        <p class="monitor__headline" aria-live="polite">{{ headline }}</p>
      </div>
      <span class="connection" :data-state="connection">{{ t(`monitor.${connection}`) }}</span>
    </div>

    <template v-if="snapshot">
      <dl class="totals" aria-live="polite">
        <div>
          <dt>{{ t('monitor.recipients') }}</dt>
          <dd>{{ formatNumber(snapshot.recipients) }}</dd>
        </div>
        <div>
          <dt>{{ t('monitor.messages') }}</dt>
          <dd>{{ formatNumber(snapshot.messages.total) }}</dd>
        </div>
        <div>
          <dt>{{ t('monitor.done') }}</dt>
          <dd>
            {{
              t('monitor.percent', { n: percent(done(snapshot.messages), snapshot.messages.total) })
            }}
          </dd>
        </div>
      </dl>

      <div
        class="bar"
        role="img"
        :aria-label="
          t('monitor.progress', { n: percent(done(snapshot.messages), snapshot.messages.total) })
        "
      >
        <span
          v-for="status in statuses"
          :key="status"
          :data-status="status"
          :style="{ width: `${percent(snapshot.messages[status], snapshot.messages.total)}%` }"
        ></span>
      </div>

      <div v-if="channels.length > 0" class="channels">
        <section v-for="ch in channels" :key="ch" class="channel" :data-channel="ch">
          <h3>{{ t(`channel.${ch}`) }}</h3>
          <dl class="tally">
            <div v-for="status in statuses" :key="status" :data-status="status">
              <dt>{{ t(`delivery.${status}`) }}</dt>
              <dd>{{ formatNumber(snapshot.by_channel[ch]?.[status] ?? 0) }}</dd>
            </div>
          </dl>
        </section>
      </div>
      <p v-else class="hint">{{ t('monitor.noMessages') }}</p>

      <ul class="legend">
        <li v-for="status in statuses" :key="status" :data-status="status">
          <strong>{{ t(`delivery.${status}`) }}</strong> — {{ t(`delivery.${status}Hint`) }}
        </li>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.monitor {
  display: grid;
  gap: 18px;
}

.monitor__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.monitor__headline {
  margin-top: 4px;
  color: var(--text-muted);
}

.connection {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  color: var(--text-subtle);
  font-size: 0.8125rem;
  font-weight: 550;
}

.connection::before {
  content: '';
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: var(--border-strong);
}

.connection[data-state='live']::before {
  background: var(--success);
}

.connection[data-state='reconnecting']::before {
  background: var(--warning);
}

.connection[data-state='final']::before {
  background: var(--neutral);
}

.totals {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
  margin: 0;
}

.totals div {
  padding: 10px 12px;
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
}

.totals dt,
.tally dt {
  color: var(--text-subtle);
  font-size: 0.75rem;
}

.totals dd {
  margin: 0;
  font-size: 1.4rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.bar {
  display: flex;
  height: 10px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--neutral-soft);
}

.channels {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 12px;
}

.channel {
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
}

.tally {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 6px;
  margin: 10px 0 0;
}

.tally div {
  padding-left: 8px;
  border-left: 3px solid var(--status-colour);
}

.tally dd {
  margin: 0;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
}

.legend {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
  color: var(--text-muted);
  font-size: 0.8125rem;
}

.legend li::before {
  content: '';
  display: inline-block;
  width: 8px;
  height: 8px;
  margin-right: 8px;
  border-radius: 2px;
  background: var(--status-colour);
}

[data-status='delivered'] {
  --status-colour: var(--success);
}

[data-status='accepted'] {
  --status-colour: var(--info);
}

[data-status='pending'] {
  --status-colour: #98a2b3;
}

[data-status='failed'] {
  --status-colour: var(--danger);
}

[data-status='skipped'] {
  --status-colour: var(--warning);
}

.bar span {
  background: var(--status-colour);
}

@media (max-width: 640px) {
  .tally {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
</style>
