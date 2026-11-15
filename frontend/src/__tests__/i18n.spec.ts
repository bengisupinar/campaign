import { beforeEach, describe, expect, it, vi } from 'vitest'

import en from '@/locales/en'
import tr from '@/locales/tr'

// Each test imports i18n afresh, as a page load would, after preparing localStorage.
async function load() {
  vi.resetModules()
  return import('@/i18n')
}

describe('i18n', () => {
  beforeEach(() => localStorage.clear())

  it('is English by default', async () => {
    const { locale, t } = await load()
    expect(locale.value).toBe('en')
    expect(t('nav.campaigns')).toBe('Campaigns')
  })

  it('switches language and remembers it for the next visit', async () => {
    const i18n = await load()
    i18n.setLocale('tr')
    expect(i18n.t('nav.campaigns')).toBe('Kampanyalar')
    expect(localStorage.getItem('locale')).toBe('tr')

    const { locale } = await load()
    expect(locale.value).toBe('tr')
  })

  it('ignores a stored value that is no language it has', async () => {
    localStorage.setItem('locale', 'fr')
    const { locale } = await load()
    expect(locale.value).toBe('en')
  })

  it('fills parameters and keeps placeholders as written', async () => {
    const { t } = await load()
    expect(t('notice.deleted', { name: 'Spring sale' })).toBe('“Spring sale” deleted.')
    expect(t('problem.malformed_placeholder')).toContain('{{first_name}}')
  })

  it('formats numbers, money and lists in the current language', async () => {
    const i18n = await load()
    expect(i18n.formatNumber(12_345)).toBe('12,345')
    expect(i18n.formatMoney(100_000)).toBe('₺1,000.00')
    expect(i18n.formatList(['SMS', 'Email'])).toBe('SMS and Email')
    i18n.setLocale('tr')
    expect(i18n.formatNumber(12_345)).toBe('12.345')
    expect(i18n.formatMoney(100_000)).toBe('₺1.000,00')
    expect(i18n.formatList(['SMS', 'E-posta'])).toBe('SMS ve E-posta')
    expect(i18n.countryName('DE')).toBe('Almanya')
  })

  it('has every text in both languages', () => {
    expect(Object.keys(tr).sort()).toEqual(Object.keys(en).sort())
    expect(Object.values(tr).filter((text) => text.trim() === '')).toEqual([])
  })
})
