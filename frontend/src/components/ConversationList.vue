<script setup lang="ts">
import { computed, ref } from 'vue'
import { useChatStore } from '@/stores/chat'
import { usePeersStore } from '@/stores/peers'
import { useAuthStore } from '@/stores/auth'
import type { Conversation } from '@/lib/types'

const chatStore = useChatStore()
const peersStore = usePeersStore()
const authStore = useAuthStore()

const searchQuery = ref('')
const showTeamConversations = ref(true)
const showPrivateConversations = ref(true)

const filteredConversations = computed(() => {
  let convs = chatStore.conversations
  
  // Filter by search
  if (searchQuery.value) {
    const query = searchQuery.value.toLowerCase()
    convs = convs.filter(c => 
      (c.display_name || '').toLowerCase().includes(query) ||
      (c.peer_pubkey || '').toLowerCase().includes(query)
    )
  }
  
  return convs
})

const teamConversations = computed(() => 
  filteredConversations.value.filter(c => c.type === 'team')
)

const privateConversations = computed(() => 
  filteredConversations.value.filter(c => c.type === 'private')
)

function selectConversation(conv: Conversation) {
  chatStore.selectConversation(conv.id)
}

function getConversationDisplay(conv: Conversation): string {
  if (conv.type === 'team') {
    return conv.display_name || 'Team Chat'
  }
  if (conv.peer_pubkey) {
    return conv.display_name || truncateKey(conv.peer_pubkey)
  }
  return conv.display_name || 'Private Chat'
}

function truncateKey(key: string, length = 12): string {
  if (!key) return ''
  if (key.length <= length) return key
  return key.slice(0, length) + '…'
}

function truncateMessage(msg: string, maxLength = 40): string {
  if (!msg) return 'No messages yet'
  if (msg.length <= maxLength) return msg
  return msg.slice(0, maxLength) + '…'
}

function formatTime(timestamp: string | null): string {
  if (!timestamp) return ''
  
  const date = new Date(timestamp)
  const now = new Date()
  const diff = now.getTime() - date.getTime()
  
  // Less than 24 hours ago
  if (diff < 86400000) {
    return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  }
  
  // Less than 7 days ago
  if (diff < 604800000) {
    return date.toLocaleDateString([], { weekday: 'short' })
  }
  
  // Older
  return date.toLocaleDateString([], { month: 'short', day: 'numeric' })
}

function getConversationStatus(conv: Conversation): 'team' | 'online' | 'offline' {
  if (conv.type === 'team') return 'team'
  
  // Find peer status
  const peer = peersStore.getPeerByPubkey(conv.peer_pubkey)
  return peer?.status === 'online' ? 'online' : 'offline'
}

function isActive(conv: Conversation): boolean {
  return chatStore.selectedConvId === conv.id
}

async function refresh() {
  await chatStore.loadConversations()
}
</script>

<template>
  <div class="conversation-list">
    <!-- Search -->
    <div class="search-box">
      <svg class="search-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="8"/>
        <path d="M21 21l-4.35-4.35"/>
      </svg>
      <input
        v-model="searchQuery"
        type="text"
        placeholder="Search conversations..."
        class="search-input"
      />
    </div>

    <!-- Empty state -->
    <div v-if="!authStore.isAuthenticated" class="empty-state">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0110 0v4"/>
      </svg>
      <p>Login required</p>
    </div>

    <div v-else-if="filteredConversations.length === 0" class="empty-state">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/>
      </svg>
      <p>No conversations yet</p>
      <span class="empty-hint">Join a team to start chatting</span>
    </div>

    <!-- Conversations -->
    <div v-else class="conv-sections">
      <!-- Team Conversations -->
      <div v-if="teamConversations.length > 0" class="conv-section">
        <button 
          class="section-toggle"
          @click="showTeamConversations = !showTeamConversations"
        >
          <svg 
            class="toggle-icon" 
            :class="{ collapsed: !showTeamConversations }"
            width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
          >
            <polyline points="6 9 12 15 18 9"/>
          </svg>
          <span>Team</span>
          <span class="count">{{ teamConversations.length }}</span>
        </button>
        
        <div v-show="showTeamConversations" class="conv-items">
          <button
            v-for="conv in teamConversations"
            :key="conv.id"
            class="conv-item"
            :class="{ active: isActive(conv) }"
            @click="selectConversation(conv)"
          >
            <div class="conv-icon team">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
                <circle cx="9" cy="7" r="4"/>
                <path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/>
              </svg>
            </div>
            <div class="conv-content">
              <div class="conv-header">
                <span class="conv-name">{{ getConversationDisplay(conv) }}</span>
                <span v-if="conv.last_msg_time" class="conv-time">{{ formatTime(conv.last_msg_time) }}</span>
              </div>
              <div class="conv-preview">{{ truncateMessage(conv.last_message) }}</div>
            </div>
            <div class="status-indicator team"></div>
          </button>
        </div>
      </div>

      <!-- Private Conversations -->
      <div v-if="privateConversations.length > 0" class="conv-section">
        <button 
          class="section-toggle"
          @click="showPrivateConversations = !showPrivateConversations"
        >
          <svg 
            class="toggle-icon" 
            :class="{ collapsed: !showPrivateConversations }"
            width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"
          >
            <polyline points="6 9 12 15 18 9"/>
          </svg>
          <span>Private</span>
          <span class="count">{{ privateConversations.length }}</span>
        </button>
        
        <div v-show="showPrivateConversations" class="conv-items">
          <button
            v-for="conv in privateConversations"
            :key="conv.id"
            class="conv-item"
            :class="{ active: isActive(conv) }"
            @click="selectConversation(conv)"
          >
            <div class="conv-icon private" :class="getConversationStatus(conv)">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
            </div>
            <div class="conv-content">
              <div class="conv-header">
                <span class="conv-name">{{ getConversationDisplay(conv) }}</span>
                <span v-if="conv.last_msg_time" class="conv-time">{{ formatTime(conv.last_msg_time) }}</span>
              </div>
              <div class="conv-preview">{{ truncateMessage(conv.last_message) }}</div>
            </div>
            <div class="status-indicator" :class="getConversationStatus(conv)"></div>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.conversation-list {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: hidden;
}

/* Search Box */
.search-box {
  position: relative;
  margin-bottom: var(--space-3);
}

.search-icon {
  position: absolute;
  left: var(--space-3);
  top: 50%;
  transform: translateY(-50%);
  color: var(--text-muted);
  pointer-events: none;
}

.search-input {
  width: 100%;
  padding: var(--space-2) var(--space-3) var(--space-2) 36px;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: var(--text-sm);
  transition: all var(--transition-fast);
}

.search-input:focus {
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.search-input::placeholder {
  color: var(--text-muted);
}

/* Empty State */
.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: var(--space-8);
  color: var(--text-muted);
  text-align: center;
}

.empty-state svg {
  opacity: 0.5;
}

.empty-state p {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
}

.empty-hint {
  font-size: var(--text-xs);
  opacity: 0.7;
}

/* Sections */
.conv-sections {
  flex: 1;
  overflow-y: auto;
  padding-right: var(--space-1);
}

.conv-section {
  margin-bottom: var(--space-3);
}

.section-toggle {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  padding: var(--space-2);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--text-muted);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.section-toggle:hover {
  background: var(--bg-elevated);
  color: var(--text-secondary);
}

.toggle-icon {
  transition: transform var(--transition-fast);
}

.toggle-icon.collapsed {
  transform: rotate(-90deg);
}

.count {
  margin-left: auto;
  padding: 2px 6px;
  background: var(--bg-elevated);
  border-radius: var(--radius-full);
  font-size: 10px;
}

/* Conversation Items */
.conv-items {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin-top: var(--space-1);
}

.conv-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  background: transparent;
  border-radius: var(--radius-md);
  text-align: left;
  transition: all var(--transition-fast);
  position: relative;
}

.conv-item:hover {
  background: var(--bg-elevated);
}

.conv-item.active {
  background: var(--bg-elevated);
  box-shadow: inset 3px 0 0 var(--primary);
}

/* Conv Icon */
.conv-icon {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  background: var(--bg-overlay);
  color: var(--text-secondary);
  flex-shrink: 0;
}

.conv-icon.team {
  background: linear-gradient(135deg, var(--primary-muted), var(--primary-glow));
  color: var(--primary);
}

.conv-icon.private.online {
  background: linear-gradient(135deg, var(--success-muted), rgba(16, 185, 129, 0.3));
  color: var(--success);
}

.conv-icon.private.offline {
  background: var(--bg-overlay);
  color: var(--text-muted);
}

/* Conv Content */
.conv-content {
  flex: 1;
  min-width: 0;
  overflow: hidden;
}

.conv-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  margin-bottom: 2px;
}

.conv-name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.conv-time {
  font-size: var(--text-xs);
  color: var(--text-muted);
  flex-shrink: 0;
}

.conv-preview {
  font-size: var(--text-xs);
  color: var(--text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* Status Indicator */
.status-indicator {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.status-indicator.team {
  background: var(--primary);
  box-shadow: 0 0 6px var(--primary-glow);
}

.status-indicator.online {
  background: var(--success);
  box-shadow: 0 0 6px rgba(16, 185, 129, 0.5);
}

.status-indicator.offline {
  background: var(--text-disabled);
}
</style>
