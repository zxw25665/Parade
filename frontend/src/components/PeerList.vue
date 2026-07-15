<script setup lang="ts">
import { ref, computed } from 'vue'
import { usePeersStore } from '@/stores/peers'
import type { PeerWithStatus } from '@/lib/types'

const emit = defineEmits<{
  connect: [ip: string]
  startPrivateChat: [peer: PeerWithStatus]
}>()

const peersStore = usePeersStore()

const showConnectDialog = ref(false)
const connectIP = ref('')
const connectError = ref('')
const isConnecting = ref(false)
const connectSave = ref(false)

const onlinePeers = computed(() => peersStore.onlinePeers)
const offlinePeers = computed(() => peersStore.offlinePeers)
const savedOfflinePeers = computed(() => 
  peersStore.savedPeers.filter(sp => {
    const discovered = peersStore.peers.find(p => p.ip === sp.ip)
    return !discovered || discovered.status === 'offline'
  })
)

function openConnectDialog() {
  showConnectDialog.value = true
  connectIP.value = ''
  connectError.value = ''
  isConnecting.value = false
  connectSave.value = false
}

function closeConnectDialog() {
  showConnectDialog.value = false
  connectIP.value = ''
  connectError.value = ''
  isConnecting.value = false
}

function validateIP(ip: string): boolean {
  const ipv4Regex = /^(\d{1,3}\.){3}\d{1,3}(:\d+)?$/
  const ipv6Regex = /^\[([^\]]+)\](:\d+)?$/
  return ipv4Regex.test(ip) || ipv6Regex.test(ip)
}

async function handleConnect() {
  const ip = connectIP.value.trim()
  
  if (!ip) {
    connectError.value = 'Please enter an IP address'
    return
  }

  if (!validateIP(ip)) {
    connectError.value = 'Invalid IP address format (e.g., 192.168.1.100 or 192.168.1.100:4327)'
    return
  }

  isConnecting.value = true
  connectError.value = ''

  try {
    const result = await peersStore.connectToPeer(ip)
    if (result.phase2.success) {
      if (connectSave.value) {
        await peersStore.addSavedPeer(ip)
      }
      closeConnectDialog()
    } else {
      connectError.value = result.phase2.error || 'Connection failed'
    }
  } catch (error) {
    connectError.value = error instanceof Error ? error.message : 'Connection failed'
  } finally {
    isConnecting.value = false
  }
}

async function handleSavePeer(ip: string) {
  try {
    await peersStore.addSavedPeer(ip)
  } catch (error) {
    console.error('Failed to save peer:', error)
  }
}

async function handleRemoveSavedPeer(ip: string) {
  try {
    await peersStore.removeSavedPeer(ip)
  } catch (error) {
    console.error('Failed to remove saved peer:', error)
  }
}

async function handleReconnectSavedPeer(ip: string) {
  try {
    await peersStore.connectToPeer(ip)
  } catch (error) {
    console.error('Failed to reconnect:', error)
  }
}

function startChat(peer: PeerWithStatus) {
  emit('startPrivateChat', peer)
}

function truncateIP(ip: string): string {
  if (!ip) return ''
  if (ip.length > 20) {
    return ip.slice(0, 17) + '...'
  }
  return ip
}

function formatLastSeen(timestamp: string): string {
  if (!timestamp) return 'Never'
  
  const date = new Date(timestamp)
  const now = new Date()
  const diff = now.getTime() - date.getTime()
  
  if (diff < 60000) return 'Just now'
  if (diff < 3600000) return `${Math.floor(diff / 60000)}m ago`
  if (diff < 86400000) return `${Math.floor(diff / 3600000)}h ago`
  return date.toLocaleDateString()
}
</script>

<template>
  <div class="peer-list">
    <div v-if="peersStore.peers.length === 0 && savedOfflinePeers.length === 0" class="empty-state">
      <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
        <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
        <line x1="6" y1="6" x2="6.01" y2="6"/>
        <line x1="6" y1="18" x2="6.01" y2="18"/>
      </svg>
      <p>No peers connected</p>
      <button class="btn btn-sm btn-secondary" @click="openConnectDialog">
        Connect to peer
      </button>
    </div>

    <div v-else class="peer-sections">
      <div v-if="onlinePeers.length > 0" class="peer-section">
        <div class="section-header">
          <span class="status-dot online"></span>
          <span>Online</span>
          <span class="count">{{ onlinePeers.length }}</span>
        </div>
        
        <div class="peer-items">
          <button
            v-for="peer in onlinePeers"
            :key="peer.pubkey"
            class="peer-item"
            :class="{ saved: peersStore.isPeerSaved(peer.ip) }"
            @click="startChat(peer)"
          >
            <div class="peer-avatar">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
              <span v-if="peersStore.isPeerSaved(peer.ip)" class="saved-badge" title="Saved peer">
                <svg width="8" height="8" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M17 3H7c-1.1 0-2 .9-2 2v16l7-3 7 3V5c0-1.1-.9-2-2-2z"/>
                </svg>
              </span>
            </div>
            <div class="peer-info">
              <span class="peer-name">{{ truncateIP(peer.ip) }}</span>
              <span class="peer-meta">Online</span>
            </div>
            <div class="peer-actions">
              <button 
                class="action-btn"
                @click.stop="startChat(peer)"
                title="Start private chat"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/>
                </svg>
              </button>
            </div>
          </button>
        </div>
      </div>

      <div v-if="offlinePeers.length > 0" class="peer-section">
        <div class="section-header">
          <span class="status-dot offline"></span>
          <span>Offline</span>
          <span class="count">{{ offlinePeers.length }}</span>
        </div>
        
        <div class="peer-items">
          <button
            v-for="peer in offlinePeers"
            :key="peer.pubkey"
            class="peer-item offline"
            :class="{ saved: peersStore.isPeerSaved(peer.ip) }"
          >
            <div class="peer-avatar muted">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
              <span v-if="peersStore.isPeerSaved(peer.ip)" class="saved-badge" title="Saved peer">
                <svg width="8" height="8" viewBox="0 0 24 24" fill="currentColor">
                  <path d="M17 3H7c-1.1 0-2 .9-2 2v16l7-3 7 3V5c0-1.1-.9-2-2-2z"/>
                </svg>
              </span>
            </div>
            <div class="peer-info">
              <span class="peer-name">{{ truncateIP(peer.ip) }}</span>
              <span class="peer-meta">Last seen: {{ formatLastSeen(peer.last_online) }}</span>
            </div>
            <div class="peer-actions">
              <button 
                class="action-btn"
                @click.stop="handleSavePeer(peer.ip)"
                v-if="!peersStore.isPeerSaved(peer.ip)"
                title="Save peer"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M17 3H7c-1.1 0-2 .9-2 2v16l7-3 7 3V5c0-1.1-.9-2-2-2z"/>
                </svg>
              </button>
              <button 
                class="action-btn reconnect"
                @click.stop="handleReconnectSavedPeer(peer.ip)"
                title="Reconnect"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M23 4v6h-6M1 20v-6h6"/>
                  <path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/>
                </svg>
              </button>
            </div>
          </button>
        </div>
      </div>

      <div v-if="savedOfflinePeers.length > 0" class="peer-section saved-section">
        <div class="section-header">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
            <path d="M17 3H7c-1.1 0-2 .9-2 2v16l7-3 7 3V5c0-1.1-.9-2-2-2z"/>
          </svg>
          <span>Saved Peers</span>
          <span class="count">{{ savedOfflinePeers.length }}</span>
        </div>
        
        <div class="peer-items">
          <div
            v-for="savedPeer in savedOfflinePeers"
            :key="savedPeer.ip"
            class="peer-item saved-peer"
          >
            <div class="peer-avatar muted">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
            </div>
            <div class="peer-info">
              <span class="peer-name">{{ truncateIP(savedPeer.ip) }}</span>
              <span class="peer-meta">Not connected</span>
            </div>
            <div class="peer-actions">
              <button 
                class="action-btn"
                @click.stop="handleReconnectSavedPeer(savedPeer.ip)"
                title="Connect"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M23 4v6h-6M1 20v-6h6"/>
                  <path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/>
                </svg>
              </button>
              <button 
                class="action-btn remove"
                @click.stop="handleRemoveSavedPeer(savedPeer.ip)"
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

      <button class="connect-btn" @click="openConnectDialog">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="12" y1="5" x2="12" y2="19"/>
          <line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        Connect to peer
      </button>
    </div>

    <Teleport to="body">
      <Transition name="modal">
        <div v-if="showConnectDialog" class="modal-overlay" @click.self="closeConnectDialog">
          <div class="modal">
            <div class="modal-header">
              <h3>Connect to Peer</h3>
              <button class="close-btn" @click="closeConnectDialog">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <line x1="18" y1="6" x2="6" y2="18"/>
                  <line x1="6" y1="6" x2="18" y2="18"/>
                </svg>
              </button>
            </div>
            
            <div class="modal-body">
              <p class="modal-desc">
                Enter the IP address of a peer to connect. Both peers must be on the same network.
              </p>
              
              <div class="form-group">
                <label for="connect-ip">IP Address</label>
                <input
                  id="connect-ip"
                  v-model="connectIP"
                  type="text"
                  placeholder="192.168.1.100:4327"
                  class="input"
                  :disabled="isConnecting"
                  @keydown.enter="handleConnect"
                />
              </div>

              <label class="checkbox-group">
                <input
                  type="checkbox"
                  v-model="connectSave"
                />
                <span>Save peer for future connections</span>
              </label>
              
              <div v-if="connectError" class="error-message">
                {{ connectError }}
              </div>
            </div>
            
            <div class="modal-footer">
              <button class="btn btn-secondary" @click="closeConnectDialog" :disabled="isConnecting">
                Cancel
              </button>
              <button class="btn btn-primary" @click="handleConnect" :disabled="isConnecting">
                <svg v-if="isConnecting" class="animate-spin" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M21 12a9 9 0 11-6.219-8.56"/>
                </svg>
                {{ isConnecting ? 'Connecting...' : 'Connect' }}
              </button>
            </div>
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
.peer-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-6);
  color: var(--text-muted);
  text-align: center;
}

.empty-state svg {
  opacity: 0.5;
}

.empty-state p {
  font-size: var(--text-sm);
}

.peer-sections {
  flex: 1;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.peer-section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.saved-section .section-header {
  color: var(--primary);
}

.saved-section .section-header svg {
  opacity: 0.7;
}

.section-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  padding: var(--space-1) var(--space-2);
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.status-dot.online {
  background: var(--success);
  box-shadow: 0 0 6px rgba(16, 185, 129, 0.6);
}

.status-dot.offline {
  background: var(--text-disabled);
}

.count {
  margin-left: auto;
  padding: 2px 6px;
  background: var(--bg-elevated);
  border-radius: var(--radius-full);
  font-size: 10px;
}

.peer-items {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.peer-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-2) var(--space-3);
  background: transparent;
  border-radius: var(--radius-md);
  text-align: left;
  transition: all var(--transition-fast);
  width: 100%;
}

.peer-item:hover {
  background: var(--bg-elevated);
}

.peer-item.saved {
  background: var(--primary-glow);
}

.peer-item.saved:hover {
  background: rgba(139, 92, 246, 0.15);
}

.peer-item.offline {
  opacity: 0.6;
}

.peer-item.saved-peer {
  border: 1px dashed var(--border-default);
  background: transparent;
}

.peer-avatar {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--success-muted);
  border-radius: var(--radius-md);
  color: var(--success);
  flex-shrink: 0;
  position: relative;
}

.peer-avatar.muted {
  background: var(--bg-overlay);
  color: var(--text-muted);
}

.saved-badge {
  position: absolute;
  bottom: -2px;
  right: -2px;
  width: 14px;
  height: 14px;
  background: var(--primary);
  color: white;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
}

.peer-info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.peer-name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.peer-meta {
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.peer-actions {
  display: flex;
  gap: var(--space-1);
}

.action-btn {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.action-btn:hover {
  background: var(--bg-overlay);
  color: var(--text-primary);
}

.action-btn.reconnect:hover {
  background: var(--primary-glow);
  color: var(--primary);
}

.action-btn.remove:hover {
  background: rgba(239, 68, 68, 0.1);
  color: var(--error);
}

.connect-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2);
  margin-top: var(--space-2);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-muted);
  background: transparent;
  border: 1px dashed var(--border-default);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.connect-btn:hover {
  color: var(--primary);
  border-color: var(--primary);
  background: var(--primary-glow);
}

.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 1000;
  padding: var(--space-4);
}

.modal {
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  width: 100%;
  max-width: 400px;
  box-shadow: var(--shadow-lg);
}

.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid var(--border-default);
}

.modal-header h3 {
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  color: var(--text-primary);
  margin: 0;
}

.close-btn {
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.close-btn:hover {
  background: var(--bg-elevated);
  color: var(--text-primary);
}

.modal-body {
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.modal-desc {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  margin: 0;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.form-group label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-secondary);
}

.input {
  width: 100%;
  padding: var(--space-3);
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: var(--text-base);
  transition: all var(--transition-fast);
}

.input:focus {
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.input::placeholder {
  color: var(--text-muted);
}

.checkbox-group {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-sm);
  color: var(--text-secondary);
  cursor: pointer;
}

.checkbox-group input[type="checkbox"] {
  width: 16px;
  height: 16px;
  accent-color: var(--primary);
}

.error-message {
  padding: var(--space-3);
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.3);
  border-radius: var(--radius-md);
  color: var(--error);
  font-size: var(--text-sm);
}

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
  padding: var(--space-4);
  border-top: 1px solid var(--border-default);
}

.modal-enter-active,
.modal-leave-active {
  transition: opacity var(--transition-fast);
}

.modal-enter-active .modal,
.modal-leave-active .modal {
  transition: transform var(--transition-fast);
}

.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}

.modal-enter-from .modal {
  transform: scale(0.95) translateY(10px);
}

.modal-leave-to .modal {
  transform: scale(0.95) translateY(10px);
}

.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
