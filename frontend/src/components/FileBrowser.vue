<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useFilesStore } from '@/stores/files'
import { usePeersStore } from '@/stores/peers'
import DirectoryTree from './DirectoryTree.vue'
import ShareDirectoryModal from './ShareDirectoryModal.vue'
import type { FileEntry } from '@/lib/types'

interface Props {
  defaultTab?: 'local' | 'remote'
}

const props = withDefaults(defineProps<Props>(), {
  defaultTab: 'local',
})

const emit = defineEmits<{
  (e: 'download-start', entry: FileEntry, savePath: string): void
}>()

const filesStore = useFilesStore()
const peersStore = usePeersStore()

// Error state
const fileBrowserError = ref<string | null>(null)

// Tab state
const activeTab = ref<'local' | 'remote'>(props.defaultTab)

// Modal state
const showShareModal = ref(false)

// Local browsing state
const localPath = ref('/')
const localEntries = ref<FileEntry[]>([])

// Remote browsing state
const selectedPeerUUID = ref('')
const remotePath = ref('/')
const remoteEntries = ref<FileEntry[]>([])

// Download target for modal
const downloadTarget = ref<FileEntry | null>(null)
const savePath = ref('')
const downloadDir = ref('')

// Computed
const onlinePeers = computed(() => peersStore.onlinePeers)

const selectedPeerName = computed(() => {
  if (!selectedPeerUUID.value) return ''
  const peer = peersStore.getPeerByPubkey(selectedPeerUUID.value)
  if (!peer) return selectedPeerUUID.value.slice(0, 12) + '...'
  return `${peer.ip} (${peer.pubkey.slice(0, 8)}...)`
})

// Initialize
onMounted(async () => {
  // Get default download directory
  try {
    downloadDir.value = await filesStore.getDefaultDownloadDir()
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to get download directory'
    downloadDir.value = '/tmp'
  }

  // Load local shared directories on mount
  await browseLocalRoot()
})

// Watch tab changes to refresh content
watch(activeTab, async (tab) => {
  if (tab === 'local') {
    await browseLocalRoot()
  } else if (tab === 'remote' && selectedPeerUUID.value) {
    await browseRemoteRoot()
  }
})

// Watch peer selection changes
watch(selectedPeerUUID, async (uuid) => {
  if (uuid && activeTab.value === 'remote') {
    await browseRemoteRoot()
  }
})

// Local browsing functions
async function browseLocalRoot(): Promise<void> {
  fileBrowserError.value = null
  localPath.value = '/'
  localEntries.value = []
  try {
    const entries = await filesStore.getLocalDirectoryChildren('/')
    localEntries.value = entries
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to browse local files'
    localEntries.value = []
  }
}

async function navigateLocal(path: string): Promise<void> {
  fileBrowserError.value = null
  try {
    const entries = await filesStore.getLocalDirectoryChildren(path)
    localEntries.value = entries
    localPath.value = path
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to navigate directory'
    localEntries.value = []
  }
}

async function goLocalBack(): Promise<void> {
  if (!localPath.value || localPath.value === '/') return
  const parts = localPath.value.replace(/\\/g, '/').split('/').filter(Boolean)
  parts.pop()
  const parent = '/' + parts.join('/')
  await navigateLocal(parent || '/')
}

// Remote browsing functions
async function browseRemoteRoot(): Promise<void> {
  if (!selectedPeerUUID.value) return
  fileBrowserError.value = null
  remotePath.value = '/'
  remoteEntries.value = []
  try {
    const entries = await filesStore.getRemoteDirectoryChildren(selectedPeerUUID.value, '/')
    remoteEntries.value = entries
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to browse remote files'
    remoteEntries.value = []
  }
}

async function navigateRemote(path: string): Promise<void> {
  if (!selectedPeerUUID.value) return
  fileBrowserError.value = null
  try {
    const entries = await filesStore.getRemoteDirectoryChildren(selectedPeerUUID.value, path)
    remoteEntries.value = entries
    remotePath.value = path
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to navigate remote directory'
    remoteEntries.value = []
  }
}

async function goRemoteBack(): Promise<void> {
  if (!remotePath.value || remotePath.value === '/') return
  const parts = remotePath.value.replace(/\\/g, '/').split('/').filter(Boolean)
  parts.pop()
  const parent = '/' + parts.join('/')
  await navigateRemote(parent || '/')
}

// Download handling
function handleDownload(entry: FileEntry): void {
  downloadTarget.value = entry
  savePath.value = downloadDir.value + '/' + entry.name
}

async function startDownload(): Promise<void> {
  if (!downloadTarget.value || !selectedPeerUUID.value || !savePath.value) return
  fileBrowserError.value = null

  try {
    await filesStore.startDownload(selectedPeerUUID.value, downloadTarget.value.path, savePath.value)
    downloadTarget.value = null
    savePath.value = ''
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to start download'
  }
}

function cancelDownload(): void {
  downloadTarget.value = null
  savePath.value = ''
}

// Share directory handling
async function handleShareDirectory(path: string): Promise<void> {
  fileBrowserError.value = null
  try {
    await filesStore.shareDirectory(path)
    showShareModal.value = false
    // Refresh local directory
    await navigateLocal(localPath.value)
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to share directory'
  }
}

async function handleUnshareDirectory(path: string): Promise<void> {
  fileBrowserError.value = null
  try {
    await filesStore.unshareDirectory(path)
    // Refresh local directory
    await navigateLocal(localPath.value)
  } catch (e) {
    fileBrowserError.value = e instanceof Error ? e.message : 'Failed to unshare directory'
  }
}

// Check if a path is shared
function isShared(path: string): boolean {
  return filesStore.localShared.includes(path)
}
</script>

<template>
  <div class="file-browser">
    <!-- Error Banner -->
    <div v-if="fileBrowserError" class="error-banner">
      <span>{{ fileBrowserError }}</span>
      <button @click="fileBrowserError = null" title="Dismiss">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="18" y1="6" x2="6" y2="18"/>
          <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </div>

    <!-- Tab Header -->
    <div class="tab-header">
      <button
        class="tab-btn"
        :class="{ active: activeTab === 'local' }"
        @click="activeTab = 'local'"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/>
        </svg>
        My Files
      </button>
      <button
        class="tab-btn"
        :class="{ active: activeTab === 'remote' }"
        @click="activeTab = 'remote'"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="2"/>
          <path d="M16.24 7.76a6 6 0 010 8.49m-8.48-.01a6 6 0 010-8.49m11.31-2.82a10 10 0 010 14.14m-14.14 0a10 10 0 010-14.14"/>
        </svg>
        Remote
      </button>
    </div>

    <!-- Local Tab -->
    <div v-if="activeTab === 'local'" class="tab-content">
      <!-- Share Section -->
      <div class="share-section">
        <div class="section-header">
          <h3 class="section-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="18" cy="5" r="3"/>
              <circle cx="6" cy="12" r="3"/>
              <circle cx="18" cy="19" r="3"/>
              <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/>
              <line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
            </svg>
            Shared Folders
          </h3>
          <button class="add-share-btn" @click="showShareModal = true" title="Add shared folder">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="12" y1="5" x2="12" y2="19"/>
              <line x1="5" y1="12" x2="19" y2="12"/>
            </svg>
            Add
          </button>
        </div>

        <!-- Shared directories list -->
        <div v-if="filesStore.localShared.length > 0" class="shared-list">
          <div
            v-for="sharedPath in filesStore.localShared"
            :key="sharedPath"
            class="shared-item"
          >
            <div class="shared-icon">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z"/>
              </svg>
            </div>
            <span class="shared-path">{{ sharedPath }}</span>
            <button
              class="unshare-btn"
              @click="handleUnshareDirectory(sharedPath)"
              title="Stop sharing"
            >
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
        </div>

        <div v-else class="no-shared">
          <p>No shared folders yet</p>
          <button class="add-first-btn" @click="showShareModal = true">
            Add your first shared folder
          </button>
        </div>
      </div>

      <!-- Local Browser -->
      <div class="browse-section">
        <div class="section-header">
          <h3 class="section-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/>
            </svg>
            Browse Files
          </h3>
        </div>

        <div class="browse-area">
          <DirectoryTree
            :entries="localEntries"
            :current-path="localPath"
            :loading="filesStore.loading"
            mode="local"
            @navigate="navigateLocal"
            @back="goLocalBack"
          />
        </div>
      </div>
    </div>

    <!-- Remote Tab -->
    <div v-if="activeTab === 'remote'" class="tab-content">
      <!-- Peer Selector -->
      <div class="peer-section">
        <div class="section-header">
          <h3 class="section-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
              <circle cx="9" cy="7" r="4"/>
              <path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/>
            </svg>
            Select Peer
          </h3>
        </div>

        <div class="peer-select-wrapper">
          <select
            v-model="selectedPeerUUID"
            class="peer-select"
          >
            <option value="">Select a peer...</option>
            <option
              v-for="peer in onlinePeers"
              :key="peer.pubkey"
              :value="peer.pubkey"
            >
              {{ peer.ip }} ({{ peer.pubkey.slice(0, 8) }}...)
            </option>
          </select>
          <svg class="select-arrow" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </div>

        <div v-if="!onlinePeers.length" class="no-peers">
          <p>No peers available. Connect to peers first.</p>
        </div>
      </div>

      <!-- Remote Browser -->
      <div v-if="selectedPeerUUID" class="browse-section">
        <div class="section-header">
          <h3 class="section-title">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M17 8l-5-5-5 5M12 3v12"/>
            </svg>
            {{ selectedPeerName }}'s Files
          </h3>
        </div>

        <div class="browse-area">
          <DirectoryTree
            :entries="remoteEntries"
            :current-path="remotePath"
            :loading="filesStore.loading"
            mode="remote"
            :peer-name="selectedPeerName"
            @navigate="navigateRemote"
            @download="handleDownload"
            @back="goRemoteBack"
          />
        </div>
      </div>
    </div>

    <!-- Download Modal -->
    <Teleport to="body">
      <div v-if="downloadTarget" class="modal-overlay" @click.self="cancelDownload">
        <div class="download-modal">
          <div class="modal-header">
            <h3>Download File</h3>
            <button class="close-btn" @click="cancelDownload">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>

          <div class="modal-body">
            <div class="file-info-row">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
                <path d="M14 2H6c-1.1 0-2 .9-2 2v16c0 1.1.9 2 2 2h12c1.1 0 2-.9 2-2V8l-6-6zm-1 7V3.5L18.5 9H13z"/>
              </svg>
              <div class="file-details">
                <div class="file-name">{{ downloadTarget.name }}</div>
                <div class="file-size">{{ downloadTarget.size ? formatSize(downloadTarget.size) : 'Unknown size' }}</div>
              </div>
            </div>

            <div class="save-path-row">
              <label>Save to:</label>
              <input
                v-model="savePath"
                type="text"
                placeholder="Enter save path..."
                class="path-input"
              />
            </div>
          </div>

          <div class="modal-footer">
            <button class="cancel-btn" @click="cancelDownload">Cancel</button>
            <button
              class="download-btn"
              :disabled="!savePath"
              @click="startDownload"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3"/>
              </svg>
              Download
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Share Directory Modal -->
    <ShareDirectoryModal
      v-if="showShareModal"
      @close="showShareModal = false"
      @share="handleShareDirectory"
    />
  </div>
</template>

<script lang="ts">
// Utility functions
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
</script>

<style scoped>
.file-browser {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  background: #16162a;
  border-radius: 12px;
  overflow: hidden;
}

.tab-header {
  display: flex;
  gap: 4px;
  padding: 8px 12px;
  background: rgba(0, 0, 0, 0.2);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.tab-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: transparent;
  border: none;
  border-radius: 8px;
  color: #888;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.15s ease;
}

.tab-btn:hover {
  background: rgba(255, 255, 255, 0.05);
  color: #ccc;
}

.tab-btn.active {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.tab-content {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
  overflow: hidden;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  background: rgba(255, 255, 255, 0.02);
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
}

.section-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: #888;
  text-transform: uppercase;
  letter-spacing: 0.5px;
}

.add-share-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  background: rgba(76, 175, 80, 0.15);
  border: 1px solid rgba(76, 175, 80, 0.3);
  border-radius: 6px;
  color: #4caf50;
  font-size: 11px;
  font-weight: 500;
  transition: all 0.15s ease;
}

.add-share-btn:hover {
  background: rgba(76, 175, 80, 0.25);
  border-color: rgba(76, 175, 80, 0.5);
}

.share-section {
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
  max-height: 200px;
  overflow-y: auto;
}

.shared-list {
  padding: 8px;
}

.shared-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 8px;
  margin-bottom: 4px;
}

.shared-icon {
  color: #f0b429;
  flex-shrink: 0;
}

.shared-path {
  flex: 1;
  font-size: 13px;
  color: #ccc;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.unshare-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  padding: 0;
  background: rgba(244, 67, 54, 0.1);
  border: 1px solid rgba(244, 67, 54, 0.2);
  border-radius: 4px;
  color: #f44336;
  opacity: 0;
  transition: all 0.15s ease;
}

.shared-item:hover .unshare-btn {
  opacity: 1;
}

.unshare-btn:hover {
  background: rgba(244, 67, 54, 0.2);
  border-color: rgba(244, 67, 54, 0.4);
}

.no-shared {
  padding: 20px;
  text-align: center;
  color: #666;
  font-size: 13px;
}

.no-shared p {
  margin: 0 0 10px 0;
}

.add-first-btn {
  padding: 8px 16px;
  background: rgba(76, 175, 80, 0.15);
  border: 1px solid rgba(76, 175, 80, 0.3);
  border-radius: 6px;
  color: #4caf50;
  font-size: 12px;
  transition: all 0.15s ease;
}

.add-first-btn:hover {
  background: rgba(76, 175, 80, 0.25);
}

.browse-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.peer-section {
  padding: 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}

.peer-select-wrapper {
  position: relative;
  margin-top: 10px;
}

.peer-select {
  width: 100%;
  padding: 10px 36px 10px 12px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: #eee;
  font-size: 13px;
  cursor: pointer;
  appearance: none;
  transition: all 0.15s ease;
}

.peer-select:hover {
  border-color: rgba(255, 255, 255, 0.2);
}

.peer-select:focus {
  outline: none;
  border-color: rgba(76, 175, 80, 0.5);
  box-shadow: 0 0 0 2px rgba(76, 175, 80, 0.15);
}

.select-arrow {
  position: absolute;
  right: 12px;
  top: 50%;
  transform: translateY(-50%);
  color: #666;
  pointer-events: none;
}

.no-peers {
  margin-top: 10px;
  padding: 16px;
  background: rgba(255, 255, 255, 0.02);
  border-radius: 8px;
  text-align: center;
  color: #666;
  font-size: 12px;
}

.no-peers p {
  margin: 0;
}

.browse-area {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* Modal Styles */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  backdrop-filter: blur(4px);
}

.download-modal {
  width: 90%;
  max-width: 420px;
  background: #1e1e38;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  background: rgba(255, 255, 255, 0.02);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.modal-header h3 {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
  color: #eee;
}

.close-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  padding: 0;
  background: transparent;
  border: none;
  border-radius: 6px;
  color: #888;
  transition: all 0.15s ease;
}

.close-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #eee;
}

.modal-body {
  padding: 20px;
}

.file-info-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 10px;
  margin-bottom: 16px;
  color: #666;
}

.file-details {
  flex: 1;
  min-width: 0;
}

.file-details .file-name {
  font-size: 14px;
  font-weight: 500;
  color: #ddd;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.file-details .file-size {
  font-size: 12px;
  color: #888;
  margin-top: 2px;
}

.save-path-row {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.save-path-row label {
  font-size: 12px;
  font-weight: 500;
  color: #888;
}

.path-input {
  width: 100%;
  padding: 10px 12px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  color: #eee;
  font-size: 13px;
  font-family: monospace;
  transition: all 0.15s ease;
}

.path-input:hover {
  border-color: rgba(255, 255, 255, 0.2);
}

.path-input:focus {
  outline: none;
  border-color: rgba(76, 175, 80, 0.5);
  box-shadow: 0 0 0 2px rgba(76, 175, 80, 0.15);
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 20px;
  background: rgba(0, 0, 0, 0.1);
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.cancel-btn {
  padding: 8px 16px;
  background: transparent;
  border: 1px solid rgba(255, 255, 255, 0.15);
  border-radius: 8px;
  color: #aaa;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.15s ease;
}

.cancel-btn:hover {
  background: rgba(255, 255, 255, 0.05);
  border-color: rgba(255, 255, 255, 0.25);
  color: #eee;
}

.download-btn {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 16px;
  background: rgba(76, 175, 80, 0.2);
  border: 1px solid rgba(76, 175, 80, 0.4);
  border-radius: 8px;
  color: #4caf50;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.15s ease;
}

.download-btn:hover:not(:disabled) {
  background: rgba(76, 175, 80, 0.3);
  border-color: rgba(76, 175, 80, 0.6);
}

.download-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* Error Banner */
.error-banner {
  padding: 0.5rem 1rem;
  margin: 0.5rem;
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: 0.375rem;
  color: #ef4444;
  font-size: 0.875rem;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.error-banner button {
  margin-left: auto;
  background: none;
  border: none;
  color: inherit;
  cursor: pointer;
  opacity: 0.7;
}
.error-banner button:hover { opacity: 1; }
</style>
