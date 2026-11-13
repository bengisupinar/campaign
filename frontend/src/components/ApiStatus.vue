<script setup lang="ts">
import { onMounted, ref } from 'vue'

type Readiness = 'checking' | 'ready' | 'not-ready' | 'unreachable'

const labels: Record<Readiness, string> = {
  checking: 'API: checking…',
  ready: 'API: ready',
  'not-ready': 'API: not ready',
  unreachable: 'API: unreachable',
}

const readiness = ref<Readiness>('checking')

// /readyz answers 200 when the API and its dependencies are usable and 503 when a
// dependency is down. Anything else (e.g. a proxy's 502) means the API itself is unreachable.
async function checkReadiness(): Promise<Readiness> {
  try {
    const res = await fetch('/readyz', { headers: { Accept: 'application/json' } })
    if (res.ok) return 'ready'
    return res.status === 503 ? 'not-ready' : 'unreachable'
  } catch {
    return 'unreachable'
  }
}

onMounted(async () => {
  readiness.value = await checkReadiness()
})
</script>

<template>
  <p class="api-status" :data-state="readiness" role="status">{{ labels[readiness] }}</p>
</template>

<style scoped>
.api-status {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 0;
  padding: 0.375rem 0.75rem;
  border-radius: 999px;
  background: #f1f3f5;
  font-size: 0.875rem;
}

.api-status::before {
  content: '';
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: var(--dot, #adb5bd);
}

.api-status[data-state='ready'] {
  --dot: #2f9e44;
}

.api-status[data-state='not-ready'] {
  --dot: #f08c00;
}

.api-status[data-state='unreachable'] {
  --dot: #e03131;
}
</style>
