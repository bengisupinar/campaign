<script setup lang="ts">
// MessageEditor edits a campaign's messages: which channels it uses, and the text for each,
// with previews that fill the placeholders as delivery will.
import { computed, ref, useId } from 'vue'

import type { Content, Problem } from '@/api'
import { fill, length, limits, placeholders, problemText } from '@/campaign'
import { t } from '@/i18n'

const content = defineModel<Content>({ required: true })
const props = defineProps<{ problems: Problem[] }>()
const id = useId()

const smsBody = ref<HTMLTextAreaElement>()
const emailSubject = ref<HTMLInputElement>()
const emailBody = ref<HTMLTextAreaElement>()

// A channel turned off keeps its text here, in case it is turned on again.
let lastSMS = content.value.sms ?? { body: '' }
let lastEmail = content.value.email ?? { subject: '', body: '' }

const sample = computed(() => ({
  first_name: t('sample.firstName'),
  last_name: t('sample.lastName'),
  city: t('sample.city'),
}))

const token = (name: string) => `{{${name}}}`

function problemsAt(field: string): Problem[] {
  return props.problems.filter((p) => p.field === field)
}

function toggleSMS(on: boolean) {
  if (content.value.sms) lastSMS = content.value.sms
  content.value = { ...content.value, sms: on ? lastSMS : undefined }
}

function toggleEmail(on: boolean) {
  if (content.value.email) lastEmail = content.value.email
  content.value = { ...content.value, email: on ? lastEmail : undefined }
}

function setSMS(body: string) {
  content.value = { ...content.value, sms: { body } }
}

function setEmail(patch: { subject?: string; body?: string }) {
  content.value = {
    ...content.value,
    email: { subject: '', body: '', ...content.value.email, ...patch },
  }
}

// insert types a placeholder at the cursor of a text field, as if the marketer had typed it.
function insert(field: HTMLInputElement | HTMLTextAreaElement | undefined, name: string) {
  if (!field) return
  field.focus()
  const end = field.selectionEnd ?? field.value.length
  field.setRangeText(token(name), field.selectionStart ?? end, end, 'end')
  field.dispatchEvent(new Event('input'))
}

const invalid = (field: string) => problemsAt(field).length > 0
</script>

<template>
  <div class="messages">
    <fieldset class="card channels">
      <legend class="label">{{ t('message.channels') }}</legend>
      <div class="channels__options">
        <label class="check">
          <input
            type="checkbox"
            :checked="!!content.sms"
            @change="toggleSMS(($event.target as HTMLInputElement).checked)"
          />
          {{ t('channel.sms') }}
        </label>
        <label class="check">
          <input
            type="checkbox"
            :checked="!!content.email"
            @change="toggleEmail(($event.target as HTMLInputElement).checked)"
          />
          {{ t('channel.email') }}
        </label>
      </div>
      <p class="hint">{{ t('message.channelsHint') }}</p>
      <p v-for="p in problemsAt('content')" :key="p.problem" class="problem">
        {{ problemText(p) }}
      </p>
    </fieldset>

    <section v-if="content.sms" class="card channel">
      <div class="channel__editor">
        <h3>{{ t('channel.sms') }}</h3>
        <div class="field">
          <div class="field__top">
            <label :for="`${id}-sms`" class="label">{{ t('message.text') }}</label>
            <span
              class="counter"
              :class="{ 'counter--over': length(content.sms.body) > limits.sms }"
            >
              {{ length(content.sms.body) }} / {{ limits.sms }}
            </span>
          </div>
          <textarea
            :id="`${id}-sms`"
            ref="smsBody"
            rows="4"
            :value="content.sms.body"
            :aria-invalid="invalid('content.sms.body')"
            @input="setSMS(($event.target as HTMLTextAreaElement).value)"
          ></textarea>
          <div class="placeholders">
            <span class="hint">{{ t('message.insert') }}</span>
            <button
              v-for="name in placeholders"
              :key="name"
              type="button"
              class="chip placeholder"
              @click="insert(smsBody, name)"
            >
              {{ token(name) }}
            </button>
          </div>
          <p
            v-for="p in problemsAt('content.sms.body')"
            :key="p.problem + p.detail"
            class="problem"
          >
            {{ problemText(p) }}
          </p>
          <p class="hint">{{ t('message.smsHint') }}</p>
        </div>
      </div>
      <div class="preview">
        <span class="preview__label">{{ t('message.preview') }}</span>
        <div class="sms-bubble">{{ fill(content.sms.body, sample) || '…' }}</div>
      </div>
    </section>

    <section v-if="content.email" class="card channel">
      <div class="channel__editor">
        <h3>{{ t('channel.email') }}</h3>
        <div class="field">
          <div class="field__top">
            <label :for="`${id}-subject`" class="label">{{ t('message.subject') }}</label>
            <span
              class="counter"
              :class="{ 'counter--over': length(content.email.subject) > limits.emailSubject }"
            >
              {{ length(content.email.subject) }} / {{ limits.emailSubject }}
            </span>
          </div>
          <input
            :id="`${id}-subject`"
            ref="emailSubject"
            type="text"
            :value="content.email.subject"
            :aria-invalid="invalid('content.email.subject')"
            @input="setEmail({ subject: ($event.target as HTMLInputElement).value })"
          />
          <div class="placeholders">
            <span class="hint">{{ t('message.insert') }}</span>
            <button
              v-for="name in placeholders"
              :key="name"
              type="button"
              class="chip placeholder"
              @click="insert(emailSubject, name)"
            >
              {{ token(name) }}
            </button>
          </div>
          <p
            v-for="p in problemsAt('content.email.subject')"
            :key="p.problem + p.detail"
            class="problem"
          >
            {{ problemText(p) }}
          </p>
        </div>
        <div class="field">
          <div class="field__top">
            <label :for="`${id}-body`" class="label">{{ t('message.text') }}</label>
            <span class="counter">{{
              t('message.characters', { n: length(content.email.body) })
            }}</span>
          </div>
          <textarea
            :id="`${id}-body`"
            ref="emailBody"
            rows="6"
            :value="content.email.body"
            :aria-invalid="invalid('content.email.body')"
            @input="setEmail({ body: ($event.target as HTMLTextAreaElement).value })"
          ></textarea>
          <div class="placeholders">
            <span class="hint">{{ t('message.insert') }}</span>
            <button
              v-for="name in placeholders"
              :key="name"
              type="button"
              class="chip placeholder"
              @click="insert(emailBody, name)"
            >
              {{ token(name) }}
            </button>
          </div>
          <p
            v-for="p in problemsAt('content.email.body')"
            :key="p.problem + p.detail"
            class="problem"
          >
            {{ problemText(p) }}
          </p>
        </div>
      </div>
      <div class="preview">
        <span class="preview__label">{{ t('message.preview') }}</span>
        <div class="email-card">
          <p class="email-card__subject">{{ fill(content.email.subject, sample) || '…' }}</p>
          <p class="email-card__body">{{ fill(content.email.body, sample) || '…' }}</p>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.messages {
  display: grid;
  gap: 16px;
}

.channels {
  display: grid;
  gap: 8px;
  margin: 0;
}

.channels legend {
  float: left;
  margin-bottom: 4px;
}

.channels__options {
  display: flex;
  clear: both;
  gap: 24px;
}

.channel {
  display: grid;
  grid-template-columns: minmax(0, 3fr) minmax(0, 2fr);
  gap: 24px;
}

.channel__editor {
  display: grid;
  gap: 14px;
  align-content: start;
}

.field__top {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}

.counter {
  color: var(--text-subtle);
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
}

.counter--over {
  color: var(--danger);
  font-weight: 600;
}

.placeholders {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
}

.placeholder {
  cursor: pointer;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.placeholder:hover {
  border-color: var(--accent);
  color: var(--accent-text);
}

.preview {
  display: grid;
  gap: 8px;
  align-content: start;
}

.preview__label {
  color: var(--text-subtle);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.sms-bubble {
  max-width: 320px;
  padding: 10px 14px;
  border-radius: 18px 18px 18px 4px;
  background: #e9ecf1;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.email-card {
  display: grid;
  gap: 8px;
  padding: 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
}

.email-card__subject {
  font-weight: 650;
  overflow-wrap: anywhere;
}

.email-card__body {
  color: var(--text-muted);
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

@media (max-width: 960px) {
  .channel {
    grid-template-columns: 1fr;
  }
}
</style>
