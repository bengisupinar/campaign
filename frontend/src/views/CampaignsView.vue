<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'

import { deleteCampaign, errorText, listCampaigns, type Campaign } from '@/api'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import { formatDateTime, t } from '@/i18n'
import { notify } from '@/notify'

const campaigns = ref<Campaign[]>([])
const loading = ref(true)
const error = ref('')
const dialog = ref<InstanceType<typeof ConfirmDialog>>()

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    campaigns.value = await listCampaigns()
  } catch (err) {
    error.value = errorText(err)
  } finally {
    loading.value = false
  }
}

async function remove(c: Campaign) {
  const confirmed = await dialog.value?.ask({
    title: t('delete.title'),
    message: t('delete.message', { name: c.name }),
    confirm: t('action.delete'),
    danger: true,
  })
  if (!confirmed) return
  try {
    await deleteCampaign(c.id)
    campaigns.value = campaigns.value.filter((x) => x.id !== c.id)
    notify(t('notice.deleted', { name: c.name }))
  } catch (err) {
    notify(errorText(err), 'error')
  }
}

// when describes the moment that matters for a campaign's status.
function when(c: Campaign): string {
  switch (c.status) {
    case 'scheduled':
      return t('when.scheduled', { time: formatDateTime(c.scheduled_at ?? c.updated_at) })
    case 'sending':
      return t('when.started', { time: formatDateTime(c.started_at ?? c.updated_at) })
    case 'sent':
    case 'failed':
      return t('when.finished', { time: formatDateTime(c.finished_at ?? c.updated_at) })
    default:
      return t('when.updated', { time: formatDateTime(c.updated_at) })
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1>{{ t('campaigns.title') }}</h1>
        <p class="hint">{{ t('campaigns.subtitle') }}</p>
      </div>
      <RouterLink to="/campaigns/new" class="btn btn-primary"
        >+ {{ t('campaigns.new') }}</RouterLink
      >
    </div>

    <div class="card list">
      <div v-if="loading" class="state" aria-busy="true">
        <p>{{ t('campaigns.loading') }}</p>
      </div>
      <div v-else-if="error" class="state" role="alert">
        <p>{{ error }}</p>
        <button type="button" class="btn" @click="load">{{ t('action.retry') }}</button>
      </div>
      <div v-else-if="campaigns.length === 0" class="state">
        <h2>{{ t('campaigns.emptyTitle') }}</h2>
        <p>{{ t('campaigns.empty') }}</p>
        <RouterLink to="/campaigns/new" class="btn btn-primary">{{
          t('campaigns.new')
        }}</RouterLink>
      </div>
      <table v-else class="table">
        <thead>
          <tr>
            <th scope="col">{{ t('campaigns.name') }}</th>
            <th scope="col">{{ t('campaigns.status') }}</th>
            <th scope="col">{{ t('campaigns.channels') }}</th>
            <th scope="col">{{ t('campaigns.when') }}</th>
            <th scope="col">
              <span class="visually-hidden">{{ t('campaigns.actions') }}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in campaigns" :key="c.id">
            <td>
              <RouterLink :to="`/campaigns/${c.id}`" class="list__name">{{ c.name }}</RouterLink>
            </td>
            <td :data-label="t('campaigns.status')">
              <span class="badge" :data-status="c.status">{{ t(`status.${c.status}`) }}</span>
            </td>
            <td :data-label="t('campaigns.channels')">
              <span class="list__channels">
                <span v-for="ch in c.channels" :key="ch" class="chip">{{
                  t(`channel.${ch}`)
                }}</span>
              </span>
            </td>
            <td class="list__when">{{ when(c) }}</td>
            <td class="list__actions">
              <template v-if="c.status === 'draft'">
                <RouterLink :to="`/campaigns/${c.id}/edit`" class="btn btn-sm">{{
                  t('action.edit')
                }}</RouterLink>
                <button type="button" class="btn btn-sm btn-ghost" @click="remove(c)">
                  {{ t('action.delete') }}
                </button>
              </template>
              <RouterLink v-else :to="`/campaigns/${c.id}`" class="btn btn-sm">{{
                t('action.view')
              }}</RouterLink>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <ConfirmDialog ref="dialog" />
  </div>
</template>

<style scoped>
.list {
  padding: 0;
  overflow: hidden;
}

.list__name {
  color: var(--text);
  font-weight: 600;
}

.list__channels {
  display: inline-flex;
  gap: 4px;
}

.list__when {
  color: var(--text-muted);
  font-size: 0.875rem;
}

.list__actions {
  text-align: right;
  white-space: nowrap;
}

.list__actions > * + * {
  margin-left: 6px;
}

@media (max-width: 640px) {
  .list__actions {
    text-align: left;
  }
}
</style>
