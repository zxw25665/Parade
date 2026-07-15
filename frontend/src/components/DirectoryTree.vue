<script setup lang="ts">
import { computed } from 'vue'
import type { FileEntry } from '@/lib/types'

interface Props {
  entries: FileEntry[]
  currentPath: string
  loading?: boolean
  mode?: 'local' | 'remote'
  peerName?: string
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
  mode: 'local',
  peerName: '',
})

const emit = defineEmits<{
  (e: 'navigate', path: string): void
  (e: 'download', entry: FileEntry): void
  (e: 'back'): void
}>()

// Sort entries: folders first, then files alphabetically
const sortedEntries = computed(() => {
  return [...props.entries].sort((a, b) => {
    // Folders come first
    if (a.isDirectory && !b.isDirectory) return -1
    if (!a.isDirectory && b.isDirectory) return 1
    // Same type: sort by name
    return a.name.localeCompare(b.name)
  })
})

// Build breadcrumb from current path
const breadcrumbs = computed(() => {
  if (!props.currentPath || props.currentPath === '/') {
    return [{ name: '/', path: '/' }]
  }

  const parts = props.currentPath.split('/').filter(Boolean)
  const crumbs = [{ name: '/', path: '/' }]

  let cumPath = ''
  for (const part of parts) {
    cumPath += '/' + part
    crumbs.push({ name: part, path: cumPath })
  }

  return crumbs
})

// Format file size
function formatSize(bytes: number): string {
  if (!bytes || bytes === 0) return ''
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(size >= 100 ? 0 : 1)} ${units[i]}`
}

// Get file extension for icon
function getFileExtension(name: string): string {
  const idx = name.lastIndexOf('.')
  return idx > 0 ? name.slice(idx + 1).toLowerCase() : ''
}

// Get file type label
function getFileType(entry: FileEntry): string {
  if (entry.isDirectory) return 'Folder'
  const ext = getFileExtension(entry.name)
  if (ext) return ext.toUpperCase()
  return 'File'
}

// Handle entry click
function handleEntryClick(entry: FileEntry): void {
  if (entry.isDirectory) {
    emit('navigate', entry.path)
  } else {
    emit('download', entry)
  }
}

// Handle breadcrumb click
function handleBreadcrumbClick(path: string): void {
  emit('navigate', path)
}

// Check if we can go back
const canGoBack = computed(() => {
  return props.currentPath && props.currentPath !== '/'
})
</script>

<template>
  <div class="directory-tree">
    <!-- Header with back button and breadcrumbs -->
    <div class="tree-header">
      <button
        v-if="canGoBack"
        class="back-btn"
        @click="emit('back')"
        :disabled="loading"
        title="Go back"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M15 18l-6-6 6-6"/>
        </svg>
      </button>

      <div class="breadcrumbs">
        <span
          v-for="(crumb, idx) in breadcrumbs"
          :key="crumb.path"
          class="breadcrumb"
          @click="handleBreadcrumbClick(crumb.path)"
        >
          <span v-if="idx > 0" class="separator">/</span>
          <span class="crumb-text">{{ crumb.name }}</span>
        </span>
      </div>
    </div>

    <!-- Loading indicator -->
    <div v-if="loading" class="loading">
      <div class="spinner"></div>
      <span>Loading...</span>
    </div>

    <!-- File list -->
    <div v-else-if="sortedEntries.length > 0" class="file-list">
      <div
        v-for="entry in sortedEntries"
        :key="entry.path"
        class="file-item"
        :class="{ 'is-folder': entry.isDirectory }"
        @click="handleEntryClick(entry)"
      >
        <!-- Icon -->
        <div class="file-icon">
          <svg v-if="entry.isDirectory" width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z"/>
          </svg>
          <svg v-else width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            <path d="M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm-1 7V3.5L18.5 9H13z"/>
          </svg>
        </div>

        <!-- File info -->
        <div class="file-info">
          <div class="file-name">{{ entry.name }}</div>
          <div class="file-meta">
            <span class="file-type">{{ getFileType(entry) }}</span>
            <span v-if="!entry.isDirectory && entry.size > 0" class="file-size">
              {{ formatSize(entry.size) }}
            </span>
          </div>
        </div>

        <!-- Action -->
        <div class="file-action">
          <button
            v-if="!entry.isDirectory && mode === 'remote'"
            class="download-btn"
            @click.stop="emit('download', entry)"
            title="Download"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3"/>
            </svg>
          </button>
          <span v-else-if="entry.isDirectory" class="enter-hint">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M9 18l6-6-6-6"/>
            </svg>
          </span>
        </div>
      </div>
    </div>

    <!-- Empty state -->
    <div v-else class="empty-state">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
        <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/>
      </svg>
      <p>No files in this directory</p>
    </div>
  </div>
</template>

<style scoped>
.directory-tree {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.tree-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  background: rgba(255, 255, 255, 0.03);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.back-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: #aaa;
  transition: all 0.15s ease;
  flex-shrink: 0;
}

.back-btn:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.1);
  color: #eee;
}

.back-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.breadcrumbs {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0;
  flex: 1;
  min-width: 0;
}

.breadcrumb {
  display: flex;
  align-items: center;
  font-size: 13px;
  color: #888;
  cursor: pointer;
  padding: 2px 4px;
  border-radius: 4px;
  transition: all 0.15s ease;
}

.breadcrumb:hover .crumb-text {
  color: #eee;
  background: rgba(255, 255, 255, 0.08);
}

.breadcrumb:last-child {
  color: #eee;
  cursor: default;
}

.breadcrumb:last-child .crumb-text {
  background: transparent;
}

.separator {
  margin: 0 2px;
  color: #555;
}

.crumb-text {
  padding: 2px 6px;
  border-radius: 4px;
}

.loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 40px;
  color: #888;
  font-size: 13px;
}

.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid rgba(255, 255, 255, 0.1);
  border-top-color: #666;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.file-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px;
}

.file-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border-radius: 8px;
  cursor: pointer;
  transition: background 0.15s ease;
}

.file-item:hover {
  background: rgba(255, 255, 255, 0.05);
}

.file-item.is-folder {
  background: rgba(255, 255, 255, 0.02);
}

.file-item.is-folder:hover {
  background: rgba(255, 255, 255, 0.08);
}

.file-icon {
  flex-shrink: 0;
  color: #666;
}

.file-item.is-folder .file-icon {
  color: #f0b429;
}

.file-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.file-name {
  font-size: 14px;
  color: #ddd;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: #666;
}

.file-type {
  padding: 1px 6px;
  background: rgba(255, 255, 255, 0.06);
  border-radius: 4px;
  text-transform: uppercase;
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.5px;
}

.file-item.is-folder .file-type {
  background: rgba(240, 180, 41, 0.15);
  color: #f0b429;
}

.file-size {
  color: #888;
}

.file-action {
  flex-shrink: 0;
  display: flex;
  align-items: center;
}

.download-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  background: rgba(76, 175, 80, 0.15);
  border: 1px solid rgba(76, 175, 80, 0.3);
  border-radius: 6px;
  color: #4caf50;
  transition: all 0.15s ease;
  opacity: 0;
}

.file-item:hover .download-btn {
  opacity: 1;
}

.download-btn:hover {
  background: rgba(76, 175, 80, 0.25);
  border-color: rgba(76, 175, 80, 0.5);
}

.enter-hint {
  color: #555;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.file-item:hover .enter-hint {
  opacity: 1;
}

.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  padding: 60px 20px;
  color: #555;
}

.empty-state svg {
  opacity: 0.3;
}

.empty-state p {
  margin: 0;
  font-size: 14px;
  color: #666;
}
</style>
