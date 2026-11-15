<script setup lang="ts">
// AssistantView is the chat with the campaign assistant. The app keeps it alive while other
// pages are open, so a conversation survives a look at a draft in the editor.
import { nextTick, ref } from 'vue'

import { ApiError, errorText, sendAssistantMessage, type AssistantTurn } from '@/api'
import AssistantAction from '@/components/AssistantAction.vue'
import { t, type MessageKey } from '@/i18n'

interface Entry {
  id: number
  from: 'user' | 'assistant'
  text: string
  notice?: AssistantTurn['notice']
  actions: AssistantTurn['actions']
}

const maxLength = 2000

// Why a turn ended without the assistant's usual reply.
const noticeKeys: Record<NonNullable<AssistantTurn['notice']>, MessageKey> = {
  refused: 'assistant.refused',
  step_limit: 'assistant.stepLimit',
  interrupted: 'assistant.interrupted',
}

const entries = ref<Entry[]>([])
const conversationID = ref<string>()
const message = ref('')
const sending = ref(false)
const error = ref('')
const list = ref<HTMLElement>()
let lastID = 0

const examples = ['assistant.example1', 'assistant.example2', 'assistant.example3'] as const

async function send() {
  const text = message.value.trim()
  if (text === '' || sending.value) return
  error.value = ''
  entries.value.push({ id: ++lastID, from: 'user', text, actions: [] })
  message.value = ''
  sending.value = true
  await scrollDown()
  try {
    const turn = await sendAssistantMessage(conversationID.value, text)
    conversationID.value = turn.conversation_id
    entries.value.push({
      id: ++lastID,
      from: 'assistant',
      text: turn.reply,
      notice: turn.notice,
      actions: turn.actions,
    })
  } catch (err) {
    // The message goes back to the input, to send again as it is.
    entries.value.pop()
    message.value = text
    error.value = errorText(err)
    if (err instanceof ApiError && err.code === 'conversation_not_found')
      conversationID.value = undefined
  } finally {
    sending.value = false
    await scrollDown()
  }
}

function startOver() {
  entries.value = []
  conversationID.value = undefined
  error.value = ''
  message.value = ''
}

async function scrollDown() {
  await nextTick()
  if (list.value) list.value.scrollTop = list.value.scrollHeight
}
</script>

<template>
  <div class="page assistant">
    <div class="page-header">
      <div>
        <h1>{{ t('assistant.title') }}</h1>
        <p class="hint">{{ t('assistant.subtitle') }}</p>
      </div>
      <button v-if="entries.length > 0" type="button" class="btn" @click="startOver">
        {{ t('assistant.new') }}
      </button>
    </div>

    <div ref="list" class="card chat" aria-live="polite">
      <div v-if="entries.length === 0" class="chat__intro">
        <p class="label">{{ t('assistant.examples') }}</p>
        <button
          v-for="key in examples"
          :key="key"
          type="button"
          class="chat__example"
          @click="message = t(key)"
        >
          {{ t(key) }}
        </button>
      </div>
      <div v-for="entry in entries" :key="entry.id" class="bubble" :data-from="entry.from">
        <span class="bubble__who">{{
          entry.from === 'user' ? t('assistant.you') : t('assistant.name')
        }}</span>
        <p v-if="entry.text" class="bubble__text">{{ entry.text }}</p>
        <p v-if="entry.notice" class="hint">
          {{ t(noticeKeys[entry.notice]) }}
        </p>
        <AssistantAction v-for="(action, i) in entry.actions" :key="i" :action="action" />
      </div>
      <p v-if="sending" class="bubble bubble--thinking" data-from="assistant" aria-busy="true">
        {{ t('assistant.thinking') }}
      </p>
    </div>

    <p v-if="error" class="problem" role="alert">
      {{ error }}
      <button type="button" class="btn btn-sm" :disabled="sending" @click="send">
        {{ t('action.retry') }}
      </button>
    </p>

    <form class="composer" @submit.prevent="send">
      <label for="assistant-message" class="visually-hidden">{{ t('assistant.label') }}</label>
      <textarea
        id="assistant-message"
        v-model="message"
        rows="3"
        :maxlength="maxLength"
        :placeholder="t('assistant.placeholder')"
        @keydown.enter.exact.prevent="send"
      ></textarea>
      <button type="submit" class="btn btn-primary" :disabled="sending || message.trim() === ''">
        {{ t('assistant.send') }}
      </button>
    </form>
  </div>
</template>

<style scoped>
.chat {
  display: grid;
  align-content: start;
  gap: 14px;
  min-height: 320px;
  max-height: 60vh;
  overflow-y: auto;
}

.chat__intro {
  display: grid;
  justify-items: start;
  gap: 8px;
}

.chat__example {
  padding: 8px 12px;
  border: 1px solid var(--border);
  border-radius: var(--radius-sm);
  background: var(--surface-muted);
  color: var(--text-muted);
  text-align: left;
  cursor: pointer;
}

.chat__example:hover {
  border-color: var(--accent);
  color: var(--accent-text);
}

.bubble {
  display: grid;
  gap: 8px;
  max-width: min(680px, 90%);
  margin: 0;
}

.bubble[data-from='user'] {
  justify-self: end;
  padding: 10px 14px;
  border-radius: 14px 14px 4px 14px;
  background: var(--accent-soft);
}

.bubble__who {
  color: var(--text-subtle);
  font-size: 0.75rem;
  font-weight: 600;
}

.bubble__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.bubble--thinking {
  color: var(--text-muted);
}

.composer {
  display: grid;
  grid-template-columns: 1fr auto;
  align-items: end;
  gap: 10px;
}

.composer textarea {
  min-height: 72px;
}

@media (max-width: 640px) {
  .composer {
    grid-template-columns: 1fr;
  }
}
</style>
