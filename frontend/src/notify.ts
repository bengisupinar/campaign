// Notices: short messages about something that just happened, such as a saved draft or a failed
// request. App shows them; any page adds one with notify().

import { reactive } from 'vue'

export interface Notice {
  id: number
  text: string
  kind: 'success' | 'error'
}

const visibleFor = 6000 // milliseconds

export const notices = reactive<Notice[]>([])
let lastID = 0

export function notify(text: string, kind: Notice['kind'] = 'success') {
  const id = ++lastID
  notices.push({ id, text, kind })
  setTimeout(() => dismiss(id), visibleFor)
}

export function dismiss(id: number) {
  const i = notices.findIndex((n) => n.id === id)
  if (i >= 0) notices.splice(i, 1)
}
