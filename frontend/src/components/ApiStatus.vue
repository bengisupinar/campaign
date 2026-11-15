<script setup lang="ts">
import { onMounted, ref } from 'vue'

import { t } from '@/i18n'

type Readiness = 'checking' | 'ready' | 'notReady' | 'unreachable'

const readiness = ref<Readiness>('checking')

// /readyz answers 200 when the API and its dependencies are usable and 503 when a
// dependency is down. Anything else (e.g. a proxy's 502) means the API itself is unreachable.
async function checkReadiness(): Promise<Readiness> {
  try {
    const res = await fetch('/readyz', { headers: { Accept: 'application/json' } })
    if (res.ok) return 'ready'
    return res.status === 503 ? 'notReady' : 'unreachable'
  } catch {
    return 'unreachable'
  }
}

onMounted(async () => {
  readiness.value = await checkReadiness()
})
</script>

<template>
  <p class="api-status" :data-state="readiness" role="status">{{ t(`api.${readiness}`) }}</p>
</template>

<style scoped>
.api-status {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 0;
  padding: 0.25rem 0.625rem;
  border-radius: 999px;
  background: var(--neutral-soft, #f1f3f5);
  color: var(--text-muted, #475467);
  font-size: 0.8125rem;
  white-space: nowrap;
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

.api-status[data-state='notReady'] {
  --dot: #f08c00;
}

.api-status[data-state='unreachable'] {
  --dot: #e03131;
}
</style>
