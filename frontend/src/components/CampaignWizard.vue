<script setup lang="ts">
// CampaignWizard composes a campaign in three steps (audience, message, review) and saves or
// dispatches it. It edits the draft it is given and never loads one itself, so any draft can
// be opened in it: a new one, a saved one, or a proposal.
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, useId, watch } from 'vue'
import { onBeforeRouteLeave } from 'vue-router'

import {
  ApiError,
  createCampaign,
  dispatchCampaign,
  errorText,
  segmentOptions,
  updateCampaign,
  type Audience,
  type Campaign,
  type Draft,
  type Problem,
  type SegmentOptions,
} from '@/api'
import AudienceEstimate from '@/components/AudienceEstimate.vue'
import CampaignSummary from '@/components/CampaignSummary.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import MessageEditor from '@/components/MessageEditor.vue'
import SegmentGroup from '@/components/SegmentGroup.vue'
import {
  channelsOf,
  countConditions,
  emptyDraft,
  limits,
  problemText,
  stepOf,
  validateDraft,
  validateSchedule,
} from '@/campaign'
import { formatDateTime, formatList, formatNumber, t } from '@/i18n'
import { notify } from '@/notify'

type Step = 1 | 2 | 3

const props = defineProps<{
  initial: Draft
  campaignId?: number // the saved campaign the draft belongs to, if any
  initialStep?: Step
  initialProblems?: Problem[] // problems already known, e.g. reported for a proposal
}>()
const emit = defineEmits<{
  saved: [Campaign]
  dispatched: [Campaign]
}>()

// copy returns a deep copy of a draft, which is plain JSON (structuredClone refuses Vue's proxies).
const copy = (d: Draft): Draft => JSON.parse(JSON.stringify(d))

const draft = reactive<Draft>(copy(props.initial))
const id = ref(props.campaignId)
const step = ref<Step>(props.initialStep ?? 1)
// The furthest step that can be opened. A saved draft is complete, so all of its steps can.
const reached = ref<Step>(props.campaignId === undefined ? step.value : 3)
const checked = reactive(new Set<Step>()) // steps whose problems are shown
const savedProblems = ref<Problem[]>(props.initialProblems ?? []) // from the API, until the next change
const estimateProblems = ref<Problem[]>([])
const options = ref<SegmentOptions>()
const optionsError = ref('')
const audience = ref<Audience>()
const busy = ref(false)
const when = ref<'now' | 'later'>('now')
const scheduledAt = ref('') // a datetime-local value, in the browser's time zone
const dialog = ref<InstanceType<typeof ConfirmDialog>>()
const heading = ref<HTMLElement>()
const nameID = useId()
const scheduleID = useId()

// What was last saved, to tell unsaved changes. A new draft counts as saved while it is blank.
const savedJSON = ref(JSON.stringify(props.campaignId === undefined ? emptyDraft() : props.initial))
const dirty = computed(() => JSON.stringify(draft) !== savedJSON.value)

const stepLabels = computed(() => [t('wizard.audience'), t('wizard.message'), t('wizard.review')])
const conditionCount = computed(() => countConditions(draft.segment.conditions))
const draftProblems = computed(() => validateDraft(draft))
const scheduleProblems = computed(() =>
  when.value === 'later' ? validateSchedule(scheduledAt.value, new Date()) : [],
)
const segmentInvalid = computed(() =>
  draftProblems.value.some((p) => p.field.startsWith('segment')),
)

// The problems shown: the composer's own for the steps checked so far, and the API's.
const problems = computed(() => {
  const own = [...draftProblems.value, ...scheduleProblems.value].filter((p) =>
    checked.has(stepOf(p.field)),
  )
  const seen = new Set<string>()
  return [...own, ...savedProblems.value, ...estimateProblems.value].filter((p) => {
    const key = `${p.field}|${p.problem}|${p.detail ?? ''}`
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
})

const problemsAt = (field: string) => problems.value.filter((p) => p.field === field)
const stepHasProblems = (s: Step) => problems.value.some((p) => stepOf(p.field) === s)

// The scheduling input's range: from now to a year ahead, in local time.
const scheduleMin = computed(() => localInput(new Date()))
const scheduleMax = computed(() =>
  localInput(new Date(Date.now() + limits.scheduleAheadDays * 86_400_000)),
)
const timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone

watch(
  draft,
  () => {
    savedProblems.value = [] // the API's problems were about the draft as it was
  },
  { deep: true },
)

watch(step, async () => {
  await nextTick()
  heading.value?.focus()
})

onMounted(loadOptions)

async function loadOptions() {
  optionsError.value = ''
  try {
    options.value = await segmentOptions()
  } catch (err) {
    optionsError.value = errorText(err)
  }
}

function go(target: Step) {
  step.value = target
  if (target > reached.value) reached.value = target
}

function next() {
  checked.add(step.value)
  if (!stepHasProblems(step.value)) go((step.value + 1) as Step)
}

// check shows every problem of the given steps and opens the first step that has one.
function check(...steps: Step[]): boolean {
  steps.forEach((s) => checked.add(s))
  const first = problems.value.find((p) => steps.includes(stepOf(p.field)))
  if (first) go(stepOf(first.field))
  return first === undefined
}

async function save(): Promise<Campaign | undefined> {
  if (!check(1, 2)) return undefined
  busy.value = true
  try {
    const body = copy(draft)
    const campaign =
      id.value === undefined ? await createCampaign(body) : await updateCampaign(id.value, body)
    id.value = campaign.id
    savedJSON.value = JSON.stringify(body)
    reached.value = 3
    emit('saved', campaign)
    return campaign
  } catch (err) {
    fail(err)
    return undefined
  } finally {
    busy.value = false
  }
}

async function saveDraft() {
  if (await save()) notify(t('notice.saved'))
}

async function dispatch() {
  if (!check(1, 2, 3)) return
  const later = when.value === 'later'
  const confirmed = await dialog.value?.ask({
    title: later ? t('dispatch.scheduleTitle') : t('dispatch.sendTitle'),
    message: dispatchMessage(later),
    confirm: later ? t('dispatch.schedule') : t('dispatch.send'),
  })
  if (!confirmed) return
  const saved = await save()
  if (!saved) return
  busy.value = true
  try {
    const at = later ? new Date(scheduledAt.value).toISOString() : undefined
    emit('dispatched', await dispatchCampaign(saved.id, at))
  } catch (err) {
    fail(err)
  } finally {
    busy.value = false
  }
}

function dispatchMessage(later: boolean): string {
  const params = {
    name: draft.name.trim(),
    channels: formatList(channelsOf(draft.content).map((ch) => t(`channel.${ch}`))),
    time: later ? formatDateTime(new Date(scheduledAt.value).toISOString()) : '',
  }
  const message = later ? t('dispatch.scheduleMessage', params) : t('dispatch.sendMessage', params)
  const reach = reachOf(audience.value)
  return reach === undefined
    ? message
    : `${message} ${t('dispatch.reach', { n: formatNumber(reach) })}`
}

function reachOf(a: Audience | undefined): number | undefined {
  const channels = channelsOf(draft.content)
  if (!a || channels.length === 0) return undefined
  return channels.length > 1 ? a.reachable : a.by_channel[channels[0]!]
}

// fail shows why a save or dispatch failed: problems next to their fields, anything else as a
// notice.
function fail(err: unknown) {
  if (err instanceof ApiError && err.status === 422 && err.problems.length > 0) {
    savedProblems.value = err.problems
    go(stepOf(err.problems[0]!.field))
    notify(t('notice.fixProblems'), 'error')
    return
  }
  notify(errorText(err), 'error')
}

function localInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// Leaving with unsaved changes asks first: within the app, and when closing the tab.
onBeforeRouteLeave(async () => {
  if (!dirty.value || !dialog.value) return true
  return dialog.value.ask({
    title: t('leave.title'),
    message: t('leave.message'),
    confirm: t('leave.confirm'),
    danger: true,
  })
})

function warnBeforeUnload(event: BeforeUnloadEvent) {
  if (dirty.value) event.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', warnBeforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', warnBeforeUnload))
</script>

<template>
  <div class="wizard">
    <div class="card field">
      <label :for="nameID" class="label">{{ t('wizard.name') }}</label>
      <input
        :id="nameID"
        v-model="draft.name"
        type="text"
        :placeholder="t('wizard.namePlaceholder')"
        :aria-invalid="problemsAt('name').length > 0"
      />
      <p v-for="p in problemsAt('name')" :key="p.problem" class="problem">{{ problemText(p) }}</p>
    </div>

    <nav :aria-label="t('wizard.steps')">
      <ol class="steps">
        <li v-for="(label, i) in stepLabels" :key="i">
          <button
            type="button"
            class="steps__button"
            :class="{
              'steps__button--current': step === i + 1,
              'steps__button--problem': stepHasProblems((i + 1) as Step),
            }"
            :aria-current="step === i + 1 ? 'step' : undefined"
            :disabled="i + 1 > reached"
            @click="go((i + 1) as Step)"
          >
            <span class="steps__number">{{ i + 1 }}</span>
            {{ label }}
          </button>
        </li>
      </ol>
    </nav>

    <section class="wizard__step">
      <h2 ref="heading" tabindex="-1">{{ stepLabels[step - 1] }}</h2>

      <div v-if="step === 1" class="audience">
        <div class="card audience__builder">
          <p v-if="optionsError" class="problem">
            {{ optionsError }}
            <button type="button" class="btn btn-sm" @click="loadOptions">
              {{ t('action.retry') }}
            </button>
          </p>
          <SegmentGroup
            v-model="draft.segment"
            path="segment"
            :depth="1"
            :room="limits.conditions - conditionCount"
            :options="options"
            :problems="problems"
          />
        </div>
        <AudienceEstimate
          :segment="draft.segment"
          :paused="segmentInvalid"
          @problems="estimateProblems = $event"
          @estimate="audience = $event"
        />
      </div>

      <MessageEditor v-else-if="step === 2" v-model="draft.content" :problems="problems" />

      <div v-else class="review">
        <CampaignSummary :draft="draft" />
        <div class="review__side">
          <AudienceEstimate
            :segment="draft.segment"
            :channels="channelsOf(draft.content)"
            :paused="segmentInvalid"
            @problems="estimateProblems = $event"
            @estimate="audience = $event"
          />
          <fieldset class="card when">
            <legend class="label">{{ t('wizard.when') }}</legend>
            <label class="check"
              ><input v-model="when" type="radio" value="now" /> {{ t('wizard.now') }}</label
            >
            <label class="check"
              ><input v-model="when" type="radio" value="later" /> {{ t('wizard.later') }}</label
            >
            <div v-if="when === 'later'" class="field">
              <label :for="scheduleID" class="label">{{ t('wizard.sendAt') }}</label>
              <input
                :id="scheduleID"
                v-model="scheduledAt"
                type="datetime-local"
                :min="scheduleMin"
                :max="scheduleMax"
                :aria-invalid="problemsAt('scheduled_at').length > 0"
              />
              <p class="hint">{{ t('wizard.timeZone', { zone: timeZone }) }}</p>
              <p v-for="p in problemsAt('scheduled_at')" :key="p.problem" class="problem">
                {{ p.problem === 'invalid' ? t('problem.schedule') : problemText(p) }}
              </p>
            </div>
          </fieldset>
        </div>
      </div>
    </section>

    <div class="wizard__actions">
      <button v-if="step > 1" type="button" class="btn" @click="go((step - 1) as Step)">
        {{ t('action.back') }}
      </button>
      <span class="wizard__spacer"></span>
      <span v-if="dirty" class="hint">{{ t('wizard.unsaved') }}</span>
      <button type="button" class="btn" :disabled="busy" @click="saveDraft">
        {{ t('action.saveDraft') }}
      </button>
      <button v-if="step < 3" type="button" class="btn btn-primary" @click="next">
        {{ t('action.next') }}
      </button>
      <button v-else type="button" class="btn btn-primary" :disabled="busy" @click="dispatch">
        {{ when === 'later' ? t('dispatch.schedule') : t('dispatch.send') }}
      </button>
    </div>

    <ConfirmDialog ref="dialog" />
  </div>
</template>

<style scoped>
.wizard {
  display: grid;
  gap: 20px;
}

.steps {
  display: flex;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.steps li {
  flex: 1;
}

.steps__button {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface);
  color: var(--text-muted);
  font-weight: 550;
  text-align: left;
  cursor: pointer;
}

.steps__button:disabled {
  cursor: default;
  opacity: 0.6;
}

.steps__number {
  display: inline-grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: 50%;
  background: var(--neutral-soft);
  font-size: 0.8rem;
}

.steps__button--current {
  border-color: var(--accent);
  color: var(--accent-text);
  box-shadow: 0 0 0 3px var(--accent-soft);
}

.steps__button--current .steps__number {
  background: var(--accent);
  color: #fff;
}

.steps__button--problem .steps__number {
  background: var(--danger);
  color: #fff;
}

.wizard__step {
  display: grid;
  gap: 14px;
}

.wizard__step h2:focus {
  outline: none;
}

.audience,
.review {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 300px;
  gap: 16px;
  align-items: start;
}

.review__side {
  display: grid;
  gap: 16px;
}

.when {
  display: grid;
  gap: 10px;
  margin: 0;
}

.wizard__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding-top: 16px;
  border-top: 1px solid var(--border);
}

.wizard__spacer {
  flex: 1;
}

@media (max-width: 960px) {
  .audience,
  .review {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .steps {
    flex-direction: column;
  }

  .wizard__actions {
    position: sticky;
    bottom: 0;
    margin: 0 -16px;
    padding: 12px 16px;
    background: var(--surface);
    box-shadow: 0 -1px 0 var(--border);
  }
}
</style>
