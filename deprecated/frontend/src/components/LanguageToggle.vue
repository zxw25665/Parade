<template>
  <button
    class="lang-toggle"
    @click="toggleLocale"
    :title="currentLocale === 'zh' ? 'Switch to English' : '切换到中文'"
  >
    {{ t('lang.toggle') }}
  </button>
</template>

<script setup>
import { useI18n } from 'vue-i18n'
import { setStoredLocale } from '../i18n/index.js'

const { locale, t } = useI18n()

const currentLocale = locale

function toggleLocale() {
  const next = locale.value === 'zh' ? 'en' : 'zh'
  locale.value = next
  setStoredLocale(next)
  document.documentElement.lang = next
}
</script>

<style scoped>
.lang-toggle {
  font-size: 11px;
  font-weight: 500;
  padding: 2px 8px;
  margin-left: auto;
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xs);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all var(--transition-fast);
  white-space: nowrap;
}
.lang-toggle:hover {
  background: var(--color-surface-hover);
  color: var(--color-text);
  border-color: var(--color-accent);
}
</style>
