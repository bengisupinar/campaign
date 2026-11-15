<script setup lang="ts">
// CampaignEditView loads the draft to compose, new or saved, and opens the wizard on it.
import { onMounted, ref, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import { ApiError, errorText, getCampaign, type Campaign, type Draft } from '@/api'
import { emptyDraft } from '@/campaign'
import CampaignWizard from '@/components/CampaignWizard.vue'
import { formatDateTime, t } from '@/i18n'
import { notify } from '@/notify'

const props = defineProps<{ id?: number }>()
const route = useRoute()
const router = useRouter()

const initial = ref<Draft>()
const loaded = ref(0) // changes with each draft loaded, so the wizard starts afresh
const error = ref('')
const notFound = ref(false)
let savedID: number | undefined // set when the wizard's first save gave the draft its ID

onMounted(() => load(props.id))
watch(
  () => props.id,
  (id) => {
    if (id !== savedID) load(id) // the URL changed to another campaign, not by our save
  },
)

async function load(id: number | undefined) {
  error.value = ''
  notFound.value = false
  initial.value = undefined
  if (id === undefined) {
    initial.value = emptyDraft()
    loaded.value++
    return
  }
  try {
    const c = await getCampaign(id)
    if (c.status !== 'draft') {
      notify(t('notice.notDraft'), 'error')
      await router.replace(`/campaigns/${id}`)
      return
    }
    initial.value = { name: c.name, segment: c.segment, content: c.content }
    loaded.value++
  } catch (err) {
    notFound.value = err instanceof ApiError && err.status === 404
    error.value = errorText(err)
  }
}

// After the first save the draft has an ID: the URL follows, so a reload reopens it.
function onSaved(c: Campaign) {
  if (props.id === c.id) return
  savedID = c.id
  router.replace({ path: `/campaigns/${c.id}/edit` })
}

function onDispatched(c: Campaign) {
  notify(
    c.status === 'scheduled' && c.scheduled_at
      ? t('notice.scheduled', { time: formatDateTime(c.scheduled_at) })
      : t('notice.sending'),
  )
  router.push(`/campaigns/${c.id}`)
}
</script>

<template>
  <div class="page">
    <div>
      <p class="breadcrumb">
        <RouterLink to="/campaigns">{{ t('campaigns.title') }}</RouterLink> /
        {{ id === undefined ? t('edit.newTitle') : t('edit.title') }}
      </p>
      <h1>{{ id === undefined ? t('edit.newTitle') : t('edit.title') }}</h1>
    </div>

    <div v-if="error" class="card state" role="alert">
      <p>{{ notFound ? t('campaign.notFound') : error }}</p>
      <RouterLink v-if="notFound" to="/campaigns" class="btn">{{
        t('action.backToList')
      }}</RouterLink>
      <button v-else type="button" class="btn" @click="load(id)">{{ t('action.retry') }}</button>
    </div>
    <div v-else-if="!initial" class="card state" aria-busy="true">
      <p>{{ t('campaign.loading') }}</p>
    </div>
    <CampaignWizard
      v-else
      :key="loaded"
      :initial="initial"
      :campaign-id="id"
      :initial-step="route.query.step === '3' ? 3 : 1"
      @saved="onSaved"
      @dispatched="onDispatched"
    />
  </div>
</template>
