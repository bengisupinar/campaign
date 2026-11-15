<script setup lang="ts">
import { watchEffect } from 'vue'
import { RouterLink, RouterView } from 'vue-router'

import ApiStatus from '@/components/ApiStatus.vue'
import { locale, setLocale, t, type Locale } from '@/i18n'
import { dismiss, notices } from '@/notify'

watchEffect(() => {
  document.documentElement.lang = locale.value
})

function changeLocale(event: Event) {
  setLocale((event.target as HTMLSelectElement).value as Locale)
}
</script>

<template>
  <header class="header">
    <div class="header__inner">
      <RouterLink to="/campaigns" class="brand">{{ t('app.name') }}</RouterLink>
      <nav :aria-label="t('nav.label')" class="nav">
        <RouterLink to="/campaigns">{{ t('nav.campaigns') }}</RouterLink>
      </nav>
      <div class="header__end">
        <ApiStatus />
        <label class="visually-hidden" for="language">{{ t('language.label') }}</label>
        <select id="language" class="language" :value="locale" @change="changeLocale">
          <option value="en">English</option>
          <option value="tr">Türkçe</option>
        </select>
      </div>
    </div>
  </header>

  <main class="main">
    <RouterView />
  </main>

  <div class="notices" aria-live="polite">
    <div v-for="n in notices" :key="n.id" class="notice" :data-kind="n.kind" role="status">
      <span>{{ n.text }}</span>
      <button
        type="button"
        class="notice__close"
        :aria-label="t('action.dismiss')"
        @click="dismiss(n.id)"
      >
        ×
      </button>
    </div>
  </div>
</template>

<style scoped>
.header {
  position: sticky;
  top: 0;
  z-index: 10;
  border-bottom: 1px solid var(--border);
  background: var(--surface);
}

.header__inner {
  display: flex;
  align-items: center;
  gap: 24px;
  max-width: 1120px;
  margin: 0 auto;
  padding: 10px 24px;
}

.brand {
  color: var(--text);
  font-size: 1.05rem;
  font-weight: 700;
  letter-spacing: -0.01em;
}

.brand::before {
  content: '';
  display: inline-block;
  width: 10px;
  height: 10px;
  margin-right: 8px;
  border-radius: 3px;
  background: var(--accent);
}

.brand:hover,
.nav a:hover {
  text-decoration: none;
}

.nav a {
  color: var(--text-muted);
  font-size: 0.9rem;
  font-weight: 550;
}

.nav a.router-link-active {
  color: var(--accent-text);
}

.header__end {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-left: auto;
}

.language {
  width: auto;
  min-height: 32px;
  padding: 4px 8px;
  font-size: 0.875rem;
}

.main {
  max-width: 1120px;
  margin: 0 auto;
  padding: 28px 24px 64px;
}

.notices {
  position: fixed;
  right: 16px;
  bottom: 16px;
  z-index: 20;
  display: grid;
  gap: 8px;
  width: min(380px, calc(100vw - 32px));
}

.notice {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-left: 4px solid var(--success);
  border-radius: var(--radius-sm);
  background: var(--surface);
  box-shadow: var(--shadow-lg);
  font-size: 0.9rem;
}

.notice[data-kind='error'] {
  border-left-color: var(--danger);
}

.notice__close {
  border: 0;
  background: none;
  color: var(--text-subtle);
  font-size: 1.1rem;
  line-height: 1;
  cursor: pointer;
}

@media (max-width: 640px) {
  .header__inner {
    flex-wrap: wrap;
    gap: 8px 16px;
    padding: 10px 16px;
  }

  .main {
    padding: 20px 16px 96px;
  }
}
</style>
