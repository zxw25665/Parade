<script setup lang="ts">
import { computed } from 'vue'
import { useFilesStore, type DownloadTask } from '@/stores/files'

const filesStore = useFilesStore()

// Computed
const activeDownloads = computed(() => filesStore.activeDownloads)
const completedDownloads = computed(() => filesStore.completedDownloads)
const failedDownloads = computed(() => filesStore.failedDownloads)

// Calculate progress percentage
function getProgress(download: DownloadTask): number {
  if (!download.totalSize) return 0
  return Math.min(100, Math.round((download.transferred / download.totalSize) * 100))
}

// Format file size
function formatSize(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(size >= 100 ? 0 : 1)} ${units[i]}`
}

// Get file name from path
function getFileName(path: string): string {
  if (!path) return 'Unknown'
  return path.split('/').pop() || path.split('\\').pop() || path
}

// Calculate transfer speed (bytes per second)
function getSpeed(download: DownloadTask): string {
  if (!download.startedAt || !download.transferred) return '0 B/s'

  const elapsed = (Date.now() - new Date(download.startedAt).getTime()) / 1000
  if (elapsed <= 0) return '0 B/s'

  const speed = download.transferred / elapsed
  if (speed < 1024) return `${Math.round(speed)} B/s`
  if (speed < 1024 * 1024) return `${(speed / 1024).toFixed(1)} KB/s`
  return `${(speed / (1024 * 1024)).toFixed(1)} MB/s`
}

// Get status color
function getStatusColor(status: DownloadTask['status']): string {
  switch (status) {
    case 'completed': return '#4caf50'
    case 'failed': return '#f44336'
    case 'paused': return '#ff9800'
    case 'downloading': return '#2196f3'
    default: return '#888'
  }
}

// Get status icon
function getStatusIcon(status: DownloadTask['status']): string {
  switch (status) {
    case 'completed': return 'check'
    case 'failed': return 'x'
    case 'paused': return 'pause'
    case 'downloading': return 'download'
    default: return 'clock'
  }
}

// Remove download
function removeDownload(taskId: string): void {
  filesStore.removeDownload(taskId)
}

// Clear all completed downloads
function clearCompleted(): void {
  filesStore.clearCompletedDownloads()
}

// Open completed file (placeholder)
function openFile(download: DownloadTask): void {
  // TODO: Implement file opening via Tauri
  console.log('Open file:', download.filePath)
}
</script>

<template>
  <div class="download-list">
    <!-- Header -->
    <div class="list-header">
      <div class="header-title">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3"/>
        </svg>
        Downloads
      </div>

      <button
        v-if="completedDownloads.length > 0"
        class="clear-btn"
        @click="clearCompleted"
        title="Clear completed"
      >
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="3 6 5 6 21 6"/>
          <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2"/>
        </svg>
        Clear
      </button>
    </div>

    <!-- Active Downloads -->
    <div class="section">
      <div class="section-header">
        <span class="section-label">Active</span>
        <span class="section-count">{{ activeDownloads.length }}</span>
      </div>

      <div v-if="activeDownloads.length === 0" class="empty-state">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="12" cy="12" r="10"/>
          <polyline points="12 6 12 12 16 14"/>
        </svg>
        <p>No active downloads</p>
      </div>

      <div v-else class="download-items">
        <div
          v-for="download in activeDownloads"
          :key="download.id"
          class="download-item"
        >
          <!-- File icon and info -->
          <div class="item-icon" :class="download.status">
            <svg v-if="download.status === 'downloading'" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3"/>
            </svg>
            <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <polyline points="12 6 12 12 16 14"/>
            </svg>
          </div>

          <div class="item-info">
            <div class="item-name">{{ download.fileName }}</div>
            <div class="item-meta">
              <span class="item-size">
                {{ formatSize(download.transferred) }} / {{ formatSize(download.totalSize) }}
              </span>
              <span class="item-speed">{{ getSpeed(download) }}</span>
            </div>

            <!-- Progress bar -->
            <div class="progress-bar">
              <div
                class="progress-fill"
                :style="{ width: getProgress(download) + '%' }"
              ></div>
            </div>

            <div class="item-percent">{{ getProgress(download) }}%</div>
          </div>

          <!-- Cancel button -->
          <button
            class="cancel-btn"
            @click="removeDownload(download.taskId)"
            title="Cancel download"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"/>
              <line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>
      </div>
    </div>

    <!-- Failed Downloads -->
    <div v-if="failedDownloads.length > 0" class="section">
      <div class="section-header">
        <span class="section-label error">Failed</span>
        <span class="section-count error">{{ failedDownloads.length }}</span>
      </div>

      <div class="download-items">
        <div
          v-for="download in failedDownloads"
          :key="download.id"
          class="download-item failed"
        >
          <div class="item-icon failed">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="15" y1="9" x2="9" y2="15"/>
              <line x1="9" y1="9" x2="15" y2="15"/>
            </svg>
          </div>

          <div class="item-info">
            <div class="item-name">{{ download.fileName }}</div>
            <div class="item-error">{{ download.error || 'Download failed' }}</div>
          </div>

          <button
            class="remove-btn"
            @click="removeDownload(download.taskId)"
            title="Remove"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"/>
              <line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>
      </div>
    </div>

    <!-- Completed Downloads -->
    <div v-if="completedDownloads.length > 0" class="section">
      <div class="section-header">
        <span class="section-label success">Completed</span>
        <span class="section-count success">{{ completedDownloads.length }}</span>
      </div>

      <div class="download-items">
        <div
          v-for="download in completedDownloads"
          :key="download.id"
          class="download-item completed"
        >
          <div class="item-icon completed">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </div>

          <div class="item-info">
            <div class="item-name">{{ download.fileName }}</div>
            <div class="item-meta">
              <span class="item-size">{{ formatSize(download.totalSize) }}</span>
              <span v-if="download.completedAt" class="item-time">
                {{ new Date(download.completedAt).toLocaleTimeString() }}
              </span>
            </div>
          </div>

          <div class="item-actions">
            <button
              class="open-btn"
              @click="openFile(download)"
              title="Open file"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M18 13v6a2 2 0 01-2 2H5a2 2 0 01-2-2V8a2 2 0 012-2h6"/>
                <polyline points="15 3 21 3 21 9"/>
                <line x1="10" y1="14" x2="21" y2="3"/>
              </svg>
            </button>
            <button
              class="remove-btn"
              @click="removeDownload(download.taskId)"
              title="Remove"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.download-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: #16162a;
  border-radius: 12px;
  overflow: hidden;
}

.list-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  background: rgba(0, 0, 0, 0.2);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.header-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 14px;
  font-weight: 600;
  color: #eee;
}

.clear-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  background: transparent;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: #888;
  font-size: 11px;
  transition: all 0.15s ease;
}

.clear-btn:hover {
  background: rgba(244, 67, 54, 0.1);
  border-color: rgba(244, 67, 54, 0.3);
  color: #f44336;
}

.section {
  padding: 12px;
}

.section + .section {
  border-top: 1px solid rgba(255, 255, 255, 0.04);
}

.section-header {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 10px;
}

.section-label {
  font-size: 11px;
  font-weight: 600;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.section-label.success {
  color: #4caf50;
}

.section-label.error {
  color: #f44336;
}

.section-count {
  display: flex;
  align-items: center;
  justify-content: center;
  min-width: 20px;
  height: 20px;
  padding: 0 6px;
  background: rgba(255, 255, 255, 0.08);
  border-radius: 10px;
  font-size: 11px;
  font-weight: 600;
  color: #888;
}

.section-count.success {
  background: rgba(76, 175, 80, 0.15);
  color: #4caf50;
}

.section-count.error {
  background: rgba(244, 67, 54, 0.15);
  color: #f44336;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 30px 20px;
  color: #555;
}

.empty-state svg {
  opacity: 0.3;
}

.empty-state p {
  margin: 0;
  font-size: 13px;
  color: #666;
}

.download-items {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.download-item {
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 12px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 10px;
  border: 1px solid rgba(255, 255, 255, 0.04);
  transition: all 0.15s ease;
}

.download-item:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(255, 255, 255, 0.08);
}

.item-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  background: rgba(33, 150, 243, 0.15);
  border-radius: 8px;
  color: #2196f3;
  flex-shrink: 0;
}

.item-icon.downloading {
  background: rgba(33, 150, 243, 0.15);
  color: #2196f3;
}

.item-icon.completed {
  background: rgba(76, 175, 80, 0.15);
  color: #4caf50;
}

.item-icon.failed {
  background: rgba(244, 67, 54, 0.15);
  color: #f44336;
}

.item-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.item-name {
  font-size: 13px;
  font-weight: 500;
  color: #ddd;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.item-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  font-size: 11px;
  color: #666;
}

.item-size {
  color: #888;
}

.item-speed {
  color: #2196f3;
}

.item-percent {
  font-size: 11px;
  font-weight: 600;
  color: #2196f3;
  margin-top: 2px;
}

.item-error {
  font-size: 11px;
  color: #f44336;
}

.item-time {
  color: #666;
}

.progress-bar {
  width: 100%;
  height: 4px;
  background: rgba(255, 255, 255, 0.08);
  border-radius: 2px;
  overflow: hidden;
  margin-top: 6px;
}

.progress-fill {
  height: 100%;
  background: linear-gradient(90deg, #2196f3, #4caf50);
  border-radius: 2px;
  transition: width 0.3s ease;
}

.cancel-btn,
.remove-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: #666;
  opacity: 0;
  transition: all 0.15s ease;
  flex-shrink: 0;
}

.download-item:hover .cancel-btn,
.download-item:hover .remove-btn {
  opacity: 1;
}

.cancel-btn:hover {
  background: rgba(244, 67, 54, 0.15);
  border-color: rgba(244, 67, 54, 0.3);
  color: #f44336;
}

.remove-btn:hover {
  background: rgba(244, 67, 54, 0.15);
  border-color: rgba(244, 67, 54, 0.3);
  color: #f44336;
}

.item-actions {
  display: flex;
  gap: 4px;
  flex-shrink: 0;
}

.open-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: rgba(76, 175, 80, 0.1);
  border: 1px solid rgba(76, 175, 80, 0.2);
  border-radius: 6px;
  color: #4caf50;
  opacity: 0;
  transition: all 0.15s ease;
}

.download-item.completed:hover .open-btn {
  opacity: 1;
}

.open-btn:hover {
  background: rgba(76, 175, 80, 0.2);
  border-color: rgba(76, 175, 80, 0.4);
}
</style>
