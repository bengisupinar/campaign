<script setup lang="ts">
// CampaignView shows one campaign: its details, its draft actions, and its delivery progress.
import { onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import {
  ApiError,
  deleteCampaign,
  errorText,
  getCampaign,
  type Campaign,
  type Snapshot,
} from '@/api'
import CampaignSummary from '@/components/CampaignSummary.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DeliveryMonitor from '@/components/DeliveryMonitor.vue'
import { formatDateTime, t } from '@/i18n'
import { notify } from '@/notify'

const props = defineProps<{ id: number }>()
const router = useRouter()

const campaign = ref<Campaign>()
const error = ref('')
const notFound = ref(false)
const dialog = ref<InstanceType<typeof ConfirmDialog>>()

onMounted(load)
watch(() => props.id, load)

async function load() {
  campaign.value = undefined
  error.value = ''
  notFound.value = false
  try {
    campaign.value = await getCampaign(props.id)
  } catch (err) {
    notFound.value = err instanceof ApiError && err.status === 404
    error.value = errorText(err)
  }
}

// The live progress carries the campaign's status and times: the header follows it.
function onSnapshot(s: Snapshot) {
  if (!campaign.value) return
  Object.assign(campaign.value, {
    status: s.status,
    scheduled_at: s.scheduled_at,
    started_at: s.started_at,
    finished_at: s.finished_at,
  })
}

async function remove() {
  const c = campaign.value
  if (!c) return
  const confirmed = await dialog.value?.ask({
    title: t('delete.title'),
    message: t('delete.message', { name: c.name }),
    confirm: t('action.delete'),
    danger: true,
  })
  if (!confirmed) return
  try {
    await deleteCampaign(c.id)
    notify(t('notice.deleted', { name: c.name }))
    await router.push('/campaigns')
  } catch (err) {
    notify(errorText(err), 'error')
  }
}
</script>

<template>
  <div class="page">
    <div v-if="error" class="card state" role="alert">
      <p>{{ notFound ? t('campaign.notFound') : error }}</p>
      <RouterLink v-if="notFound" to="/campaigns" class="btn">{{
        t('action.backToList')
      }}</RouterLink>
      <button v-else type="button" class="btn" @click="load">{{ t('action.retry') }}</button>
    </div>
    <div v-else-if="!campaign" class="card state" aria-busy="true">
      <p>{{ t('campaign.loading') }}</p>
    </div>
    <template v-else>
      <div class="page-header">
        <div>
          <p class="breadcrumb">
            <RouterLink to="/campaigns">{{ t('campaigns.title') }}</RouterLink> /
            {{ campaign.name }}
          </p>
          <div class="title">
            <h1>{{ campaign.name }}</h1>
            <span class="badge" :data-status="campaign.status">{{
              t(`status.${campaign.status}`)
            }}</span>
          </div>
        </div>
        <div v-if="campaign.status === 'draft'" class="actions">
          <button type="button" class="btn btn-ghost" @click="remove">
            {{ t('action.delete') }}
          </button>
          <RouterLink :to="`/campaigns/${campaign.id}/edit`" class="btn">{{
            t('action.edit')
          }}</RouterLink>
          <RouterLink :to="`/campaigns/${campaign.id}/edit?step=3`" class="btn btn-primary">
            {{ t('campaign.reviewAndSend') }}
          </RouterLink>
        </div>
      </div>

      <dl class="card facts">
        <div>
          <dt>{{ t('campaigns.channels') }}</dt>
          <dd>
            <span v-for="ch in campaign.channels" :key="ch" class="chip">{{
              t(`channel.${ch}`)
            }}</span>
          </dd>
        </div>
        <div>
          <dt>{{ t('campaign.created') }}</dt>
          <dd>{{ formatDateTime(campaign.created_at) }}</dd>
        </div>
        <div>
          <dt>{{ t('campaign.updated') }}</dt>
          <dd>{{ formatDateTime(campaign.updated_at) }}</dd>
        </div>
        <div v-if="campaign.scheduled_at">
          <dt>{{ t('campaign.scheduledFor') }}</dt>
          <dd>{{ formatDateTime(campaign.scheduled_at) }}</dd>
        </div>
        <div v-if="campaign.started_at">
          <dt>{{ t('campaign.started') }}</dt>
          <dd>{{ formatDateTime(campaign.started_at) }}</dd>
        </div>
        <div v-if="campaign.finished_at">
          <dt>{{ t('campaign.finished') }}</dt>
          <dd>{{ formatDateTime(campaign.finished_at) }}</dd>
        </div>
      </dl>

      <DeliveryMonitor
        v-if="campaign.status !== 'draft'"
        :key="campaign.id"
        :campaign-id="campaign.id"
        @snapshot="onSnapshot"
      />
      <CampaignSummary :draft="campaign" />
    </template>

    <ConfirmDialog ref="dialog" />
  </div>
</template>

<style scoped>
.title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 16px;
  margin: 0;
}

.facts dt {
  color: var(--text-subtle);
  font-size: 0.75rem;
}

.facts dd {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: 2px 0 0;
  font-weight: 550;
}
</style>
