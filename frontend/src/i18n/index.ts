import { createI18n } from 'vue-i18n'
import type { Ref } from 'vue'
import en from './en.json'
import zh from './zh.json'

const i18n = createI18n({
  legacy: false,
  locale: 'en',
  fallbackLocale: 'en',
  messages: {
    en,
    zh,
  },
})

export type MessageSchema = typeof en

export default i18n

export function setLocale(locale: 'en' | 'zh') {
  const localeRef = i18n.global.locale as unknown as Ref<string>
  localeRef.value = locale
}

export function getLocale(): 'en' | 'zh' {
  const localeRef = i18n.global.locale as unknown as Ref<string>
  return localeRef.value as 'en' | 'zh'
}
