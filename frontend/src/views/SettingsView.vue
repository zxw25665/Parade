<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { ref } from 'vue'
import { setLocale, getLocale } from '@/i18n'
import Panel from '@/components/ui/Panel.vue'
import Button from '@/components/ui/Button.vue'

const { t } = useI18n()

const currentLocale = ref(getLocale())

function handleLocaleChange(locale: 'en' | 'zh') {
  setLocale(locale)
  currentLocale.value = locale
}
</script>

<template>
  <div class="settings-view">
    <div class="settings-header">
      <h2>{{ t('settings.title') }}</h2>
    </div>

    <div class="settings-content">
      <Panel :title="t('settings.general')" :default-open="true">
        <div class="setting-item">
          <label class="setting-label">{{ t('settings.language') }}</label>
          <div class="locale-buttons">
            <Button
              :variant="currentLocale === 'en' ? 'primary' : 'secondary'"
              size="sm"
              @click="handleLocaleChange('en')"
            >
              English
            </Button>
            <Button
              :variant="currentLocale === 'zh' ? 'primary' : 'secondary'"
              size="sm"
              @click="handleLocaleChange('zh')"
            >
              中文
            </Button>
          </div>
        </div>
      </Panel>

      <Panel :title="t('settings.about')" :default-open="true">
        <div class="about-info">
          <div class="info-row">
            <span class="info-label">{{ t('settings.version') }}</span>
            <span class="info-value">0.2.0</span>
          </div>
          <div class="info-row">
            <span class="info-label">{{ t('app.name') }}</span>
            <span class="info-value">{{ t('app.tagline') }}</span>
          </div>
        </div>
      </Panel>
    </div>
  </div>
</template>

<style scoped>
.settings-view {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.settings-header {
  padding: 1rem 1.5rem;
  border-bottom: 1px solid #374151;
}

.settings-header h2 {
  margin: 0;
  font-size: 1.25rem;
  color: #f9fafb;
}

.settings-content {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 1.5rem;
  max-width: 600px;
}

.setting-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.setting-label {
  font-size: 0.875rem;
  color: #d1d5db;
}

.locale-buttons {
  display: flex;
  gap: 0.5rem;
}

.about-info {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.info-row {
  display: flex;
  justify-content: space-between;
}

.info-label {
  font-size: 0.875rem;
  color: #9ca3af;
}

.info-value {
  font-size: 0.875rem;
  color: #f9fafb;
}
</style>
