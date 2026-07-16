<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useChatStore } from '@/stores/chat'
import { usePeersStore } from '@/stores/peers'
import { useAuthStore } from '@/stores/auth'
import MessageBubble from './MessageBubble.vue'
import ChatInput from './ChatInput.vue'

const chatStore = useChatStore()
const peersStore = usePeersStore()
const authStore = useAuthStore()

const messageListRef = ref<HTMLElement | null>(null)
const isAtBottom = ref(true)
const hasMore = ref(false)
const loadingMore = ref(false)
const limit = ref(50)
const offset = ref(0)
const sendError = ref<string | null>(null)

// Computed
const activeConv = computed(() => chatStore.selectedConversation)
const messages = computed(() => chatStore.selectedMessages)

const peerStatus = computed(() => {
  if (!activeConv.value || activeConv.value.type !== 'team') return null
  const pubkey = activeConv.value.peer_pubkey
  if (!pubkey) return null
  return peersStore.getPeerByPubkey(pubkey)
})

const placeholder = computed(() => {
  if (!activeConv.value) return 'Select a conversation'
  return activeConv.value.type === 'team' 
    ? 'Message the team...' 
    : 'Message privately...'
})

const convTitle = computed(() => {
  if (!activeConv.value) return ''
  if (activeConv.value.type === 'team') {
    return activeConv.value.display_name || 'Team Chat'
  }
  return activeConv.value.display_name || truncateKey(activeConv.value.peer_pubkey)
})

function truncateKey(key: string, len = 12): string {
  if (!key) return 'Private Chat'
  if (key.length <= len) return key
  return key.slice(0, len) + '…'
}

// Load messages when conversation changes
watch(() => chatStore.selectedConvId, async (newId) => {
  if (!newId) return
  offset.value = 0
  hasMore.value = false
  await nextTick()
  scrollToBottom('instant')
}, { immediate: true })

// Handle new messages — watch array reference changes (shallow), not deep
watch(messages, async () => {
  await nextTick()
  scrollToBottom('instant')
})

// Auto-scroll when new messages arrive at bottom
watch(() => messages.value.length, async () => {
  if (isAtBottom.value) {
    await nextTick()
    scrollToBottom('smooth')
  }
})

function handleScroll() {
  if (!messageListRef.value) return
  
  const { scrollTop, scrollHeight, clientHeight } = messageListRef.value
  isAtBottom.value = scrollHeight - scrollTop - clientHeight < 50
  
  // Check if at top for load more
  if (scrollTop < 100 && hasMore.value && !loadingMore.value) {
    loadMore()
  }
}

async function loadMore() {
  if (loadingMore.value || !hasMore.value || !chatStore.selectedConvId) return
  
  loadingMore.value = true
  const next = offset.value + limit.value
  const prevHeight = messageListRef.value?.scrollHeight ?? 0
  
  try {
    await chatStore.loadMessages(chatStore.selectedConvId, limit.value, true)
    offset.value = next
    
    await nextTick()
    
    // Maintain scroll position
    if (messageListRef.value) {
      const newHeight = messageListRef.value.scrollHeight
      messageListRef.value.scrollTop = newHeight - prevHeight
    }
  } finally {
    loadingMore.value = false
  }
}

function scrollToBottom(behavior: ScrollBehavior = 'smooth') {
  if (!messageListRef.value) return
  messageListRef.value.scrollTo({
    top: messageListRef.value.scrollHeight,
    behavior,
  })
  isAtBottom.value = true
}

async function handleSendMessage(text: string) {
  if (!activeConv.value || !text.trim()) return
  sendError.value = null
  
  try {
    if (activeConv.value.type === 'team') {
      await chatStore.sendTeamMessage(text)
    } else {
      const peerUUID = activeConv.value.peer_pubkey
      if (!peerUUID) {
        sendError.value = 'No peer pubkey for private conversation'
        return
      }
      await chatStore.sendPrivateMessage(peerUUID, text)
    }
    
    await nextTick()
    scrollToBottom('smooth')
  } catch (error) {
    sendError.value = error instanceof Error ? error.message : 'Failed to send message'
  }
}

function isOwnMessage(sender: string): boolean {
  return sender === authStore.pubKey
}

function formatConvType(type: string): string {
  return type === 'team' ? 'Team Chat' : 'Private'
}

onMounted(() => {
  // Initial scroll to bottom
  nextTick(() => scrollToBottom('instant'))
})
</script>

<template>
  <div class="chat-panel">
    <!-- Error Banner -->
    <div v-if="sendError" class="error-banner">
      <span>{{ sendError }}</span>
      <button @click="sendError = null" title="Dismiss">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="18" y1="6" x2="6" y2="18"/>
          <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
    </div>

    <!-- Empty State -->
    <div v-if="!activeConv" class="empty-state">
      <div class="empty-icon">
        <svg width="64" height="64" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1">
          <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/>
        </svg>
      </div>
      <h2 class="empty-title">Select a conversation</h2>
      <p class="empty-text">Choose a team or private conversation from the sidebar to start chatting</p>
    </div>

    <!-- Chat Area -->
    <template v-else>
      <!-- Header -->
      <header class="chat-header">
        <div class="header-content">
          <div class="conv-info">
            <div class="conv-icon" :class="activeConv.type">
              <svg v-if="activeConv.type === 'team'" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
                <circle cx="9" cy="7" r="4"/>
                <path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/>
              </svg>
              <svg v-else width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2"/>
                <circle cx="12" cy="7" r="4"/>
              </svg>
            </div>
            <div class="conv-details">
              <h2 class="conv-title">{{ convTitle }}</h2>
              <div class="conv-meta">
                <span class="conv-type">{{ formatConvType(activeConv.type) }}</span>
                <span v-if="activeConv.type === 'private' && peerStatus" class="peer-status">
                  <span class="status-dot" :class="peerStatus.status"></span>
                  {{ peerStatus.status === 'online' ? peerStatus.ip : 'Offline' }}
                </span>
              </div>
            </div>
          </div>
        </div>
        
        <div class="header-actions">
          <button class="btn btn-ghost btn-icon" title="Search messages">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="8"/>
              <path d="M21 21l-4.35-4.35"/>
            </svg>
          </button>
        </div>
      </header>

      <!-- Messages -->
      <div 
        ref="messageListRef"
        class="message-list"
        @scroll="handleScroll"
      >
        <!-- Load More -->
        <div class="load-more">
          <button 
            v-if="hasMore"
            class="btn btn-ghost btn-sm"
            :disabled="loadingMore"
            @click="loadMore"
          >
            <svg v-if="loadingMore" class="animate-spin" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M21 12a9 9 0 11-6.219-8.56"/>
            </svg>
            {{ loadingMore ? 'Loading...' : 'Load more messages' }}
          </button>
          <span v-else class="hint">Beginning of conversation</span>
        </div>

        <!-- Empty Messages -->
        <div v-if="messages.length === 0 && !chatStore.loadingMessages" class="no-messages">
          <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z"/>
          </svg>
          <p>No messages yet</p>
          <span>Start the conversation!</span>
        </div>

        <!-- Messages -->
        <TransitionGroup name="message" tag="div" class="messages">
          <MessageBubble
            v-for="(message, index) in messages"
            :key="message.id"
            :message="message"
            :is-own="isOwnMessage(message.sender)"
            :show-sender="index === 0 || messages[index - 1]?.sender !== message.sender"
          />
        </TransitionGroup>
      </div>

      <!-- Input -->
      <ChatInput
        :placeholder="placeholder"
        :disabled="false"
        @send="handleSendMessage"
      />

      <!-- Scroll to Bottom Button -->
      <Transition name="fade">
        <button 
          v-if="!isAtBottom && messages.length > 0"
          class="scroll-btn"
          @click="scrollToBottom('smooth')"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
          <span>New messages</span>
        </button>
      </Transition>
    </template>
  </div>
</template>

<style scoped>
.chat-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-base);
  position: relative;
}

/* Empty State */
.empty-state {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-8);
  text-align: center;
  animation: fadeIn var(--transition-slow) ease-out;
}

.empty-icon {
  color: var(--text-disabled);
  opacity: 0.5;
  animation: float 3s ease-in-out infinite;
}

@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-10px); }
}

.empty-title {
  font-size: var(--text-xl);
  font-weight: var(--font-semibold);
  color: var(--text-primary);
}

.empty-text {
  font-size: var(--text-sm);
  color: var(--text-muted);
  max-width: 300px;
}

/* Header */
.chat-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-surface);
  border-bottom: 1px solid var(--border-default);
  min-height: 64px;
}

.header-content {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex: 1;
  min-width: 0;
}

.conv-info {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.conv-icon {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--radius-lg);
  flex-shrink: 0;
}

.conv-icon.team {
  background: linear-gradient(135deg, var(--primary-muted), var(--primary-glow));
  color: var(--primary);
}

.conv-icon.private {
  background: linear-gradient(135deg, var(--success-muted), rgba(16, 185, 129, 0.3));
  color: var(--success);
}

.conv-details {
  min-width: 0;
}

.conv-title {
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  margin: 0;
}

.conv-meta {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin-top: 2px;
}

.conv-type {
  padding: 2px 8px;
  background: var(--bg-elevated);
  border-radius: var(--radius-full);
}

.peer-status {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
}

.status-dot.online {
  background: var(--success);
  box-shadow: 0 0 4px rgba(16, 185, 129, 0.6);
}

.status-dot.offline {
  background: var(--text-disabled);
}

.header-actions {
  display: flex;
  gap: var(--space-1);
}

/* Message List */
.message-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.load-more {
  text-align: center;
  padding: var(--space-3);
}

.load-more .hint {
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.no-messages {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  color: var(--text-muted);
  padding: var(--space-8);
  text-align: center;
}

.no-messages svg {
  opacity: 0.5;
}

.no-messages p {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
}

.no-messages span {
  font-size: var(--text-xs);
}

.messages {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

/* Message Transitions */
.message-enter-active {
  animation: messageIn 0.3s ease-out;
}

.message-leave-active {
  animation: messageOut 0.2s ease-in;
}

@keyframes messageIn {
  from {
    opacity: 0;
    transform: translateY(12px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

@keyframes messageOut {
  from {
    opacity: 1;
    transform: translateY(0);
  }
  to {
    opacity: 0;
    transform: translateY(-8px);
  }
}

/* Scroll Button */
.scroll-btn {
  position: absolute;
  bottom: 80px;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  font-size: var(--text-sm);
  box-shadow: var(--shadow-lg);
  transition: all var(--transition-fast);
}

.scroll-btn:hover {
  background: var(--primary);
  border-color: var(--primary);
  color: white;
}

/* Fade Transition */
.fade-enter-active,
.fade-leave-active {
  transition: opacity var(--transition-fast);
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

/* Animations */
.animate-spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
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
