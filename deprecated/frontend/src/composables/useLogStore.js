import { reactive } from 'vue'

export const logStore = reactive({
  entries: [],
  maxEntries: 5000
})

export function addLogEntry(entry) {
  logStore.entries.unshift({
    time: entry.time || '',
    level: entry.level != null ? entry.level : 3,
    source: entry.source || 'unknown',
    message: entry.message || ''
  })
  if (logStore.entries.length > logStore.maxEntries) {
    logStore.entries.length = logStore.maxEntries
  }
}

export function clearLogs() {
  logStore.entries.length = 0
}
