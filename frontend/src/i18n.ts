// Interface languages: English (the default) and Turkish. Texts live in flat dictionaries keyed
// like "campaigns.title"; en.ts is the reference, and tr.ts must have exactly its keys.

import { ref } from 'vue'

import en from '@/locales/en'
import tr from '@/locales/tr'

export type Locale = 'en' | 'tr'
export type MessageKey = keyof typeof en

const dictionaries: Record<Locale, Record<MessageKey, string>> = { en, tr }
const storageKey = 'locale'

// locale is the current interface language. Templates that call t() re-render when it changes.
export const locale = ref<Locale>(storedLocale())

function storedLocale(): Locale {
  try {
    const stored = localStorage.getItem(storageKey)
    return stored === 'en' || stored === 'tr' ? stored : 'en'
  } catch {
    return 'en' // storage can be unavailable, e.g. when the browser blocks it
  }
}

// setLocale switches the interface language and remembers it for the next visit.
export function setLocale(next: Locale) {
  locale.value = next
  try {
    localStorage.setItem(storageKey, next)
  } catch {
    // Not remembered: the choice lasts until the page is reloaded.
  }
}

// t returns the text for key in the current language, with each {name} replaced by
// params[name]. Placeholders without a parameter, such as {{first_name}}, are kept as written.
export function t(key: MessageKey, params: Record<string, string | number> = {}): string {
  return dictionaries[locale.value][key].replace(/\{(\w+)\}/g, (match, name: string) =>
    name in params ? String(params[name]) : match,
  )
}

// isMessageKey tells whether key, built from a code the API returned, has a text.
export function isMessageKey(key: string): key is MessageKey {
  return Object.hasOwn(en, key)
}

export function formatDateTime(iso: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(
    new Date(iso),
  )
}

export function formatNumber(n: number): string {
  return new Intl.NumberFormat(locale.value).format(n)
}

// formatList joins items as a sentence would: "SMS and Email", "SMS ve E-posta".
export function formatList(items: string[]): string {
  return new Intl.ListFormat(locale.value, { type: 'conjunction' }).format(items)
}

// formatMoney formats an amount in kuruş as Turkish lira.
export function formatMoney(kurus: number): string {
  return new Intl.NumberFormat(locale.value, {
    style: 'currency',
    currency: 'TRY',
    currencyDisplay: 'narrowSymbol',
  }).format(kurus / 100)
}

// countryName names an ISO 3166-1 country code in the current language.
export function countryName(code: string): string {
  return displayName('region', code)
}

// languageName names an ISO 639-1 language code in the current language.
export function languageName(code: string): string {
  return displayName('language', code)
}

function displayName(type: 'region' | 'language', code: string): string {
  try {
    return new Intl.DisplayNames([locale.value], { type }).of(code) ?? code
  } catch {
    return code // not a code Intl knows
  }
}
