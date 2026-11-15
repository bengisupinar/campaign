<script setup lang="ts">
import { ref, useId } from 'vue'

import { t } from '@/i18n'

export interface Question {
  title: string
  message: string
  confirm: string // the confirming button's label
  danger?: boolean
}

const dialog = ref<HTMLDialogElement>()
const question = ref<Question>()
const titleID = useId()
let answer: ((confirmed: boolean) => void) | undefined

// ask shows the question in a modal dialog and resolves to whether it was confirmed. Escape and
// Cancel resolve to false.
function ask(q: Question): Promise<boolean> {
  answer?.(false) // a question still open is cancelled
  question.value = q
  dialog.value?.showModal()
  return new Promise((resolve) => {
    answer = resolve
  })
}

function close(confirmed: boolean) {
  dialog.value?.close()
  answer?.(confirmed)
  answer = undefined
}

defineExpose({ ask })
</script>

<template>
  <dialog ref="dialog" class="dialog" :aria-labelledby="titleID" @cancel.prevent="close(false)">
    <template v-if="question">
      <h2 :id="titleID">{{ question.title }}</h2>
      <p class="dialog__message">{{ question.message }}</p>
      <div class="dialog__actions">
        <button type="button" class="btn" autofocus @click="close(false)">
          {{ t('action.cancel') }}
        </button>
        <button
          type="button"
          class="btn"
          :class="question.danger ? 'btn-danger' : 'btn-primary'"
          data-test="confirm"
          @click="close(true)"
        >
          {{ question.confirm }}
        </button>
      </div>
    </template>
  </dialog>
</template>

<style scoped>
.dialog {
  width: min(460px, calc(100vw - 32px));
  padding: 24px;
  border: 0;
  border-radius: var(--radius);
  box-shadow: var(--shadow-lg);
  color: var(--text);
}

.dialog::backdrop {
  background: rgb(16 24 40 / 45%);
}

.dialog__message {
  margin-top: 8px;
  color: var(--text-muted);
}

.dialog__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 24px;
}
</style>
