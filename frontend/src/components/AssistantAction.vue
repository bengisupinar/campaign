<script setup lang="ts">
// AssistantAction shows one thing the assistant did as a card: a saved draft, an audience
// estimate, or a dispatch waiting for the user's decision. Approving here is the only way the
// assistant's dispatch happens.
import { computed, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { decideApproval, errorText, type AssistantAction, type Decision } from '@/api'
import { formatDateTime, formatNumber, t } from '@/i18n'

const props = defineProps<{ action: AssistantAction }>()

const deciding = ref(false)
const decision = ref<Decision>()
const error = ref('')

const c = computed(() => props.action.campaign)
const approval = computed(() => props.action.approval)

async function decide(choice: 'approve' | 'cancel') {
  const id = approval.value?.id
  if (!id) return
  deciding.value = true
  error.value = ''
  try {
    decision.value = await decideApproval(id, choice)
  } catch (err) {
    error.value = errorText(err)
  } finally {
    deciding.value = false
  }
}
</script>

<template>
  <div class="card action">
    <template v-if="action.type === 'campaign_created' || action.type === 'campaign_updated'">
      <p class="action__kind">
        {{ action.type === 'campaign_created' ? t('assistant.created') : t('assistant.updated') }}
      </p>
      <div v-if="c" class="action__title">
        <strong>{{ c.name }}</strong>
        <span class="badge" :data-status="c.status">{{ t(`status.${c.status}`) }}</span>
        <span v-for="ch in c.channels" :key="ch" class="chip">{{ t(`channel.${ch}`) }}</span>
      </div>
      <div v-if="c" class="action__links">
        <RouterLink :to="`/campaigns/${c.id}/edit`" class="btn btn-sm">{{
          t('assistant.openEditor')
        }}</RouterLink>
        <RouterLink :to="`/campaigns/${c.id}`" class="btn btn-sm btn-ghost">{{
          t('action.view')
        }}</RouterLink>
      </div>
    </template>

    <template v-else-if="action.type === 'audience_estimated' && action.audience">
      <p class="action__kind">
        {{ t('assistant.estimate') }}<template v-if="c"> · {{ c.name }}</template>
      </p>
      <dl class="action__numbers">
        <div>
          <dt>{{ t('estimate.matched') }}</dt>
          <dd>{{ formatNumber(action.audience.matched) }}</dd>
        </div>
        <div>
          <dt>{{ t('estimate.byEmail') }}</dt>
          <dd>{{ formatNumber(action.audience.by_channel.email) }}</dd>
        </div>
        <div>
          <dt>{{ t('estimate.bySMS') }}</dt>
          <dd>{{ formatNumber(action.audience.by_channel.sms) }}</dd>
        </div>
      </dl>
    </template>

    <template v-else-if="action.type === 'dispatch_requested' && c && approval">
      <p class="action__kind">
        {{ approval.scheduled_at ? t('assistant.scheduleTitle') : t('assistant.sendTitle') }}
      </p>
      <div class="action__title">
        <strong>{{ c.name }}</strong>
        <span v-for="ch in c.channels" :key="ch" class="chip">{{ t(`channel.${ch}`) }}</span>
      </div>
      <dl class="action__numbers">
        <div>
          <dt>{{ t('assistant.reach') }}</dt>
          <dd>{{ formatNumber(approval.reach) }}</dd>
        </div>
        <div>
          <dt>{{ t('assistant.when') }}</dt>
          <dd>
            {{ approval.scheduled_at ? formatDateTime(approval.scheduled_at) : t('assistant.now') }}
          </dd>
        </div>
      </dl>

      <p v-if="decision?.status === 'cancelled'" class="action__result" role="status">
        {{ t('assistant.cancelled') }}
      </p>
      <p v-else-if="decision" class="action__result action__result--done" role="status">
        {{
          decision.campaign?.status === 'scheduled' && decision.campaign.scheduled_at
            ? t('assistant.scheduled', { time: formatDateTime(decision.campaign.scheduled_at) })
            : t('assistant.dispatched')
        }}
        <RouterLink :to="`/campaigns/${c.id}`">{{ t('assistant.follow') }}</RouterLink>
      </p>
      <template v-else>
        <p class="hint">{{ t('assistant.approvalNote') }}</p>
        <div class="action__links">
          <button
            type="button"
            class="btn btn-primary btn-sm"
            :disabled="deciding"
            @click="decide('approve')"
          >
            {{ t('assistant.approve') }}
          </button>
          <button
            type="button"
            class="btn btn-ghost btn-sm"
            :disabled="deciding"
            @click="decide('cancel')"
          >
            {{ t('action.cancel') }}
          </button>
        </div>
      </template>
      <p v-if="error" class="problem" role="alert">{{ error }}</p>
    </template>
  </div>
</template>

<style scoped>
.action {
  display: grid;
  gap: 10px;
  padding: 14px 16px;
}

.action__kind {
  color: var(--text-subtle);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.action__title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}

.action__links {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.action__numbers {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: 8px;
  margin: 0;
}

.action__numbers div {
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
}

.action__numbers dt {
  color: var(--text-subtle);
  font-size: 0.75rem;
}

.action__numbers dd {
  margin: 0;
  font-weight: 650;
}

.action__result {
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  background: var(--neutral-soft);
}

.action__result--done {
  background: var(--success-soft);
  color: var(--success);
}

.action__result a {
  margin-left: 6px;
}
</style>
