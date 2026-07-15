<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useFilesStore } from '@/stores/files'
import Panel from '@/components/ui/Panel.vue'

const { t } = useI18n()
const authStore = useAuthStore()
const filesStore = useFilesStore()
</script>

<template>
  <div class="files-view">
    <div class="files-header">
      <h2>{{ t('files.title') }}</h2>
    </div>
    <div class="files-container">
      <aside class="files-sidebar">
        <Panel :title="t('files.sharedDirectories')" :default-open="true">
          <div v-if="filesStore.localShared.length === 0" class="empty-state">
            {{ t('files.noShared') }}
          </div>
          <div v-for="path in filesStore.localShared" :key="path" class="share-item">
            {{ path }}
          </div>
        </Panel>

        <Panel :title="t('files.downloads')" :default-open="true">
          <div v-if="filesStore.downloads.length === 0" class="empty-state">
            {{ t('files.noDownloads') }}
          </div>
          <div v-for="task in filesStore.downloads" :key="task.id" class="download-item">
            <span class="download-name">{{ task.fileName }}</span>
            <span class="download-status">{{ task.status }}</span>
          </div>
        </Panel>
      </aside>

      <main class="files-main">
        <div class="empty-state">
          {{ t('files.remoteFiles') }}
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.files-view {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.files-header {
  padding: 1rem 1.5rem;
  border-bottom: 1px solid #374151;
}

.files-header h2 {
  margin: 0;
  font-size: 1.25rem;
  color: #f9fafb;
}

.files-container {
  flex: 1;
  display: grid;
  grid-template-columns: 320px 1fr;
  overflow: hidden;
}

.files-sidebar {
  border-right: 1px solid #374151;
  overflow-y: auto;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.empty-state {
  padding: 1rem;
  text-align: center;
  color: #6b7280;
  font-size: 0.875rem;
}

.share-item,
.download-item {
  padding: 0.5rem;
  font-size: 0.875rem;
  color: #d1d5db;
  word-break: break-all;
}

.download-name {
  flex: 1;
}

.download-status {
  color: #6b7280;
  font-size: 0.75rem;
}

.files-main {
  overflow: auto;
  padding: 1rem;
}
</style>
