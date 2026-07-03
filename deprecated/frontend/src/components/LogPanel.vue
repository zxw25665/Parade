<template>
  <div class="log-panel">
    <div class="row row-wrap">
      <select v-model="filterLevel">
        <option v-for="opt in levelOptions" :key="opt.value" :value="opt.value">
          {{ opt.label }}
        </option>
      </select>
      <select v-model="filterSource">
        <option value="">{{ $t('logs.allSources') }}</option>
        <option v-for="src in availableSources" :key="src" :value="src">{{ src }}</option>
      </select>
      <label><input type="checkbox" v-model="autoScroll" /> {{ $t('logs.autoScroll') }}</label>
      <button class="btn-ghost btn-sm" @click="clearLogs">{{ $t('logs.clear') }}</button>
      <button class="btn-ghost btn-sm" @click="exportLogs">{{ $t('logs.export') }}</button>
    </div>
    <div ref="logList" class="log-list list">
      <div v-if="filteredLogs.length === 0" class="empty-state">{{ $t('logs.noLogs') }}</div>
      <div v-for="(entry, idx) in filteredLogs" :key="idx" class="log-entry" :class="'log-level-' + entry.level">
        <span class="log-time">{{ entry.time }}</span>
        <span class="log-level-badge badge" :class="levelBadgeClass(entry.level)">{{ levelName(entry.level) }}</span>
        <span class="log-source badge">{{ entry.source }}</span>
        <span class="log-msg">{{ entry.message }}</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { logStore, clearLogs } from '../composables/useLogStore.js'

const { t } = useI18n()

async function exportLogs() {
  const app = window.go?.app?.App
  let content
  if (app?.ExportLogs) {
    try {
      const result = await app.ExportLogs()
      content = result.content
    } catch {
      content = JSON.stringify(logStore.entries, null, 2)
    }
  } else {
    content = JSON.stringify(logStore.entries, null, 2)
  }
  const blob = new Blob([content], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'parade-logs-' + new Date().toISOString().slice(0, 10) + '.json'
  link.click()
  URL.revokeObjectURL(url)
}

const filterLevel = ref(0) // 0 = All
const filterSource = ref('')
const autoScroll = ref(true)
const logList = ref(null)

const levelOptions = [
  { value: 0, label: t('logs.levelAll') || 'All' },
  { value: 5, label: t('logs.levelError') },
  { value: 4, label: t('logs.levelWarning') },
  { value: 3, label: t('logs.levelInfo') },
  { value: 2, label: t('logs.levelDebug') },
  { value: 1, label: t('logs.levelTrace') }
]

const availableSources = computed(() => {
  const sources = new Set()
  for (const e of logStore.entries) {
    if (e.source) sources.add(e.source)
  }
  return [...sources].sort()
})

const filteredLogs = computed(() => {
  let entries = logStore.entries
  if (filterLevel.value > 0) {
    entries = entries.filter(e => e.level >= filterLevel.value)
  }
  if (filterSource.value) {
    entries = entries.filter(e => e.source === filterSource.value)
  }
  return entries
})

function levelName(level) {
  const names = {
    1: t('logs.levelTrace'),
    2: t('logs.levelDebug'),
    3: t('logs.levelInfo'),
    4: t('logs.levelWarning'),
    5: t('logs.levelError')
  }
  return names[level] || 'Unknown'
}

function levelBadgeClass(level) {
  if (level >= 5) return 'badge-danger'
  if (level >= 4) return 'badge-accent'
  return ''
}

watch(filteredLogs, () => {
  if (autoScroll.value && logList.value) {
    nextTick(() => {
      logList.value.scrollTop = 0
    })
  }
})
</script>
