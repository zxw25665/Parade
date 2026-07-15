<script setup lang="ts">
import { ref, computed } from 'vue'
import { useFilesStore } from '@/stores/files'

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'share', path: string): void
}>()

const filesStore = useFilesStore()

// Path input
const directoryPath = ref('')
const isValidPath = ref(false)
const validationError = ref('')

// Path input validation
function validatePath(path: string): void {
  if (!path.trim()) {
    isValidPath.value = false
    validationError.value = ''
    return
  }

  // Check if path looks valid
  const trimmed = path.trim()
  if (trimmed.length < 1) {
    isValidPath.value = false
    validationError.value = 'Path is too short'
    return
  }

  // Check if already shared
  if (filesStore.localShared.includes(trimmed)) {
    isValidPath.value = false
    validationError.value = 'This directory is already shared'
    return
  }

  // Basic path validation (no dangerous characters)
  if (trimmed.includes('..') && !trimmed.startsWith('/')) {
    isValidPath.value = false
    validationError.value = 'Invalid path: parent directory references not allowed'
    return
  }

  isValidPath.value = true
  validationError.value = ''
}

// Quick paths for common locations
const quickPaths = [
  { label: 'Home', path: '/home' },
  { label: 'Documents', path: '~/Documents' },
  { label: 'Downloads', path: '~/Downloads' },
  { label: 'Desktop', path: '~/Desktop' },
]

// Handle share button click
async function handleShare(): Promise<void> {
  if (!isValidPath.value) return

  const path = directoryPath.value.trim()
  emit('share', path)
}

// Handle quick path selection
function selectQuickPath(path: string): void {
  // Expand ~ to home directory
  if (path.startsWith('~/')) {
    // Use common Linux home directory path
    const homeDir = '/home/user'
    directoryPath.value = path.replace('~', homeDir)
  } else {
    directoryPath.value = path
  }
  validatePath(directoryPath.value)
}

// Close on escape key
function handleKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    emit('close')
  }
}
</script>

<template>
  <Teleport to="body">
    <div class="modal-overlay" @click.self="emit('close')" @keydown="handleKeydown">
      <div class="share-modal">
        <!-- Header -->
        <div class="modal-header">
          <div class="header-icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="18" cy="5" r="3"/>
              <circle cx="6" cy="12" r="3"/>
              <circle cx="18" cy="19" r="3"/>
              <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/>
              <line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
            </svg>
          </div>
          <div class="header-text">
            <h3>Share Directory</h3>
            <p>Add a local folder to share with peers</p>
          </div>
          <button class="close-btn" @click="emit('close')">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <line x1="18" y1="6" x2="6" y2="18"/>
              <line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>

        <!-- Body -->
        <div class="modal-body">
          <!-- Quick paths -->
          <div class="quick-paths">
            <span class="quick-label">Quick select:</span>
            <div class="quick-buttons">
              <button
                v-for="qp in quickPaths"
                :key="qp.path"
                class="quick-btn"
                @click="selectQuickPath(qp.path)"
              >
                {{ qp.label }}
              </button>
            </div>
          </div>

          <!-- Path input -->
          <div class="path-input-group">
            <label for="dir-path">Directory path</label>
            <div class="input-wrapper">
              <svg class="input-icon" width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z"/>
              </svg>
              <input
                id="dir-path"
                v-model="directoryPath"
                type="text"
                class="path-input"
                :class="{ error: validationError, valid: isValidPath }"
                placeholder="/path/to/directory"
                @input="validatePath(directoryPath)"
                @keydown.enter="handleShare"
              />
            </div>
            <div v-if="validationError" class="validation-error">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="10"/>
                <line x1="12" y1="8" x2="12" y2="12"/>
                <line x1="12" y1="16" x2="12.01" y2="16"/>
              </svg>
              {{ validationError }}
            </div>
            <div v-else-if="isValidPath" class="validation-hint">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M22 11.08V12a10 10 0 11-5.93-9.14"/>
                <polyline points="22 4 12 14.01 9 11.01"/>
              </svg>
              Ready to share
            </div>
          </div>

          <!-- Currently shared directories -->
          <div v-if="filesStore.localShared.length > 0" class="current-shared">
            <span class="current-label">Currently shared:</span>
            <div class="shared-list">
              <div
                v-for="path in filesStore.localShared"
                :key="path"
                class="shared-item"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M10 4H4c-1.1 0-2 .9-2 2v12c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V8c0-1.1-.9-2-2-2h-8l-2-2z"/>
                </svg>
                <span class="shared-path">{{ path }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Footer -->
        <div class="modal-footer">
          <button class="cancel-btn" @click="emit('close')">Cancel</button>
          <button
            class="share-btn"
            :disabled="!isValidPath"
            @click="handleShare"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="18" cy="5" r="3"/>
              <circle cx="6" cy="12" r="3"/>
              <circle cx="18" cy="19" r="3"/>
              <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/>
              <line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
            </svg>
            Share Directory
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
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

.share-modal {
  width: 90%;
  max-width: 480px;
  background: #1e1e38;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px;
  overflow: hidden;
  box-shadow: 0 20px 60px rgba(0, 0, 0, 0.5);
}

.modal-header {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 20px;
  background: rgba(255, 255, 255, 0.02);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.header-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 44px;
  height: 44px;
  background: rgba(76, 175, 80, 0.15);
  border-radius: 12px;
  color: #4caf50;
  flex-shrink: 0;
}

.header-text {
  flex: 1;
  min-width: 0;
}

.header-text h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
  color: #eee;
}

.header-text p {
  margin: 4px 0 0 0;
  font-size: 13px;
  color: #888;
}

.close-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  padding: 0;
  background: transparent;
  border: none;
  border-radius: 8px;
  color: #666;
  transition: all 0.15s ease;
  flex-shrink: 0;
}

.close-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #eee;
}

.modal-body {
  padding: 20px;
}

.quick-paths {
  margin-bottom: 16px;
}

.quick-label {
  display: block;
  font-size: 12px;
  color: #666;
  margin-bottom: 8px;
}

.quick-buttons {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.quick-btn {
  padding: 6px 12px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 6px;
  color: #aaa;
  font-size: 12px;
  transition: all 0.15s ease;
}

.quick-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  border-color: rgba(255, 255, 255, 0.2);
  color: #eee;
}

.path-input-group {
  margin-bottom: 16px;
}

.path-input-group label {
  display: block;
  font-size: 12px;
  font-weight: 500;
  color: #888;
  margin-bottom: 8px;
}

.input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.input-icon {
  position: absolute;
  left: 12px;
  color: #666;
  pointer-events: none;
}

.path-input {
  width: 100%;
  padding: 12px 12px 12px 40px;
  background: rgba(255, 255, 255, 0.05);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 10px;
  color: #eee;
  font-size: 14px;
  font-family: monospace;
  transition: all 0.15s ease;
}

.path-input::placeholder {
  color: #555;
}

.path-input:hover {
  border-color: rgba(255, 255, 255, 0.2);
}

.path-input:focus {
  outline: none;
  border-color: rgba(76, 175, 80, 0.5);
  box-shadow: 0 0 0 3px rgba(76, 175, 80, 0.15);
}

.path-input.error {
  border-color: rgba(244, 67, 54, 0.5);
}

.path-input.error:focus {
  box-shadow: 0 0 0 3px rgba(244, 67, 54, 0.15);
}

.path-input.valid {
  border-color: rgba(76, 175, 80, 0.5);
}

.validation-error,
.validation-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 8px;
  font-size: 12px;
}

.validation-error {
  color: #f44336;
}

.validation-hint {
  color: #4caf50;
}

.current-shared {
  padding-top: 16px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}

.current-label {
  display: block;
  font-size: 12px;
  color: #666;
  margin-bottom: 8px;
}

.shared-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 120px;
  overflow-y: auto;
}

.shared-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 10px;
  background: rgba(255, 255, 255, 0.03);
  border-radius: 6px;
  color: #f0b429;
}

.shared-path {
  font-size: 12px;
  font-family: monospace;
  color: #aaa;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
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
  padding: 10px 18px;
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

.share-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 18px;
  background: rgba(76, 175, 80, 0.2);
  border: 1px solid rgba(76, 175, 80, 0.4);
  border-radius: 8px;
  color: #4caf50;
  font-size: 13px;
  font-weight: 500;
  transition: all 0.15s ease;
}

.share-btn:hover:not(:disabled) {
  background: rgba(76, 175, 80, 0.3);
  border-color: rgba(76, 175, 80, 0.6);
}

.share-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}
</style>
