<template>

    <!-- Active -->
    <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">Active ({{ activeCount }})</h3>
    <div v-if="activeCount === 0" style="font-size:13px;color:#8a8aaf">No active downloads</div>
    <div v-for="(d, id) in downloads" :key="id" style="margin-bottom:12px;padding:8px;background:#16213e;border-radius:4px">
      <div style="font-size:12px;display:flex;justify-content:space-between">
        <span>{{ getFileName(d.filePath) }}</span>
        <span>{{ formatSize(d.transferred) }} / {{ formatSize(d.totalSize) }} ({{ percent(d) }}%)</span>
      </div>
      <div class="progress-bar">
        <div class="progress-fill" :style="{ width: percent(d) + '%' }"></div>
      </div>
      <div style="font-size:11px;color:#8a8aaf">{{ d.taskId }}</div>
    </div>

    <!-- Completed -->
    <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">Completed ({{ completed.length }})</h3>
    <div v-if="completed.length === 0" style="font-size:13px;color:#8a8aaf">No completed downloads</div>
    <div class="list-item" v-for="d in completed" :key="d.taskId" style="font-size:12px">
      <span class="badge badge-green">done</span>
      {{ getFileName(d.filePath) }} — {{ formatSize(d.totalSize) }}
    </div>
</template>

<script setup>
import { computed, inject } from 'vue'

const state = inject('events')
const downloads = computed(() => state.downloads)
const completed = computed(() => state.completedDownloads)
const activeCount = computed(() => Object.keys(state.downloads).length)

function percent(d) {
  if (!d.totalSize) return 0
  return Math.min(100, Math.round((d.transferred / d.totalSize) * 100))
}

function formatSize(bytes) {
  if (!bytes) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let s = bytes
  while (s >= 1024 && i < u.length - 1) { s /= 1024; i++ }
  return s.toFixed(1) + ' ' + u[i]
}

function getFileName(path) {
  if (!path) return 'unknown'
  return path.split('/').pop() || path.split('\\').pop() || path
}
</script>
