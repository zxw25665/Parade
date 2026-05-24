import { createI18n } from 'vue-i18n'
import en from './locales/en.json'
import zh from './locales/zh.json'

const STORAGE_KEY = 'parade-locale'

function detectLocale() {
  const stored = localStorage.getItem(STORAGE_KEY)
  if (stored === 'zh' || stored === 'en') return stored

  const navLang = (navigator.language || navigator.languages?.[0] || '').toLowerCase()
  if (navLang.startsWith('zh')) return 'zh'
  return 'en'
}

export function getStoredLocale() {
  return detectLocale()
}

export function setStoredLocale(locale) {
  localStorage.setItem(STORAGE_KEY, locale)
}

const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: 'en',
  messages: {
    en,
    zh
  }
})

export default i18n
