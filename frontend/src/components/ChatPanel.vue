<template>
  <div class="chat-panel">
    <div v-if="!activeConv" class="empty-state">
      <div>{{ $t('chat.selectConversation') }}</div>
    </div>

    <template v-else>
      <div class="chat-header">
        <span class="chat-icon">{{ activeConv.type === 'team' ? '👥' : '👤' }}</span>
        <span class="chat-title">{{ activeConv.display_name || convFallbackName(activeConv) }}</span>
        <span v-if="activeConv.type === 'private' && peerStatus" class="chat-peer-status" :class="peerStatus.status">
          {{ peerStatus.status === 'online' ? '🟢' : '🔴' }} {{ peerStatus.ip || '' }}
        </span>
      </div>

      <div class="message-list list" ref="msgList">
        <div class="load-more-row">
          <button
            v-if="hasMore"
            class="btn-sm btn-ghost"
            :disabled="loadingMore"
            @click="loadMore"
          >
            {{ loadingMore ? $t('chat.loading') : $t('chat.loadMore') }}
          </button>
          <span v-else class="hint">{{ $t('chat.startOfHistory') }}</span>
        </div>
        <div v-if="messages.length === 0" class="empty-state" style="padding: 24px 12px">
          {{ $t('chat.noMessages') }}
        </div>
        <div
          v-for="msg in messages"
          :key="msg.id"
          class="message-item"
          :class="{ self: msg.direction === 'send' }"
        >
          <div>
            <span class="message-sender">{{ shortSender(msg.sender) }}</span>
            <span class="message-meta">{{ msg.hlc }}</span>
          </div>
          <div class="message-body">{{ msg.content }}</div>
        </div>
      </div>

      <div class="chat-input-area">
        <button class="btn-sm btn-ghost" style="margin-right:4px" @click="dumpMessages" :disabled="!activeConv">📋</button>
        <input
          ref="inputRef"
          v-model="text"
          :placeholder="placeholder"
          @keyup.enter="doSend"
          :disabled="sending"
        />
        <button
          class="btn-primary"
          :disabled="!text.trim() || sending"
          @click="doSend"
        >{{ $t('chat.send') }}</button>
      </div>
      <div v-if="errorMsg" class="error" style="padding: 0 12px 8px">{{ errorMsg }}</div>
    </template>
  </div>
</template>

<script setup>
import { ref, computed, watch, inject, nextTick, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'
import { EventsOn, EventsOff } from '../lib/wailsjs/runtime/runtime'

const store = inject('store')
const { t } = useI18n()
const { getConversationMessages, sendTeamChat, sendPrivateChat } = useBackend()

const text = ref('')
const sending = ref(false)
const errorMsg = ref('')
const limit = ref(100)
const offset = ref(0)
const hasMore = ref(false)
const loadingMore = ref(false)
const msgList = ref(null)
const inputRef = ref(null)

const activeConv = computed(() =>
  (store.conversations || []).find(c => c.id === store.activeConversationId) || null
)

const messages = computed(() => {
  const id = store.activeConversationId
  if (!id) return []
  return store.messagesByConv[id] || []
})

const peerStatus = computed(() => {
  if (!activeConv.value || activeConv.value.type !== 'private') return null
  const pubkey = activeConv.value.peer_pubkey
  return (store.peersWithStatus || []).find(p => p.pubkey === pubkey) || null
})

const placeholder = computed(() =>
  activeConv.value && activeConv.value.type === 'private'
    ? t('chat.typePrivate')
    : t('chat.typeMessage')
)

watch(() => store.activeConversationId, async (newId) => {
  if (!newId) return
  offset.value = 0
  hasMore.value = false
  if (!store.messagesByConv[newId]) {
    await loadFromBackend(0, false)
  }
  await nextTick()
  scrollToBottom()
})

onMounted(() => {
  EventsOn('ui_conversation_updated', () => {
    if (store.activeConversationId) {
      delete store.messagesByConv[store.activeConversationId]
      loadFromBackend(0, false)
    }
  })
})
onUnmounted(() => {
  EventsOff('ui_conversation_updated')
})

async function loadFromBackend(off, replace) {
  if (!store.activeConversationId) return
  try {
    const result = await getConversationMessages(store.activeConversationId, limit.value, off)
    const list = Array.isArray(result) ? result : []
    if (replace || off === 0) {
      const normalised = list.map(m => ({
        id: m.id,
        hlc: m.hlc,
        sender: m.sender,
        content: m.content,
        timestamp: m.timestamp,
        conversationId: m.conversation_id,
        direction: m.sender === store.pubkey ? 'send' : 'receive'
      })).sort((a, b) => (a.hlc || '').localeCompare(b.hlc || ''))
      store.messagesByConv[store.activeConversationId] = normalised
    } else {
      const existing = store.messagesByConv[store.activeConversationId] || []
      const normalised = list.map(m => ({
        id: m.id,
        hlc: m.hlc,
        sender: m.sender,
        content: m.content,
        timestamp: m.timestamp,
        conversationId: m.conversation_id,
        direction: m.sender === store.pubkey ? 'send' : 'receive'
      }))
      store.messagesByConv[store.activeConversationId] = [...normalised, ...existing]
    }
    hasMore.value = list.length >= limit.value
  } catch (e) {
    errorMsg.value = String(e)
  }
}

async function loadMore() {
  if (loadingMore.value) return
  loadingMore.value = true
  const next = offset.value + limit.value
  const prevHeight = msgList.value ? msgList.value.scrollHeight : 0
  try {
    await loadFromBackend(next, false)
    offset.value = next
  } finally {
    loadingMore.value = false
    await nextTick()
    if (msgList.value) {
      msgList.value.scrollTop = msgList.value.scrollHeight - prevHeight
    }
  }
}

function shortSender(s) {
  if (!s) return 'me'
  if (s === store.pubkey) return 'me'
  return s.length > 12 ? s.slice(0, 8) + '…' : s
}

function convFallbackName(c) {
  if (c.type === 'team') return 'Team'
  if (c.peer_pubkey) return c.peer_pubkey.slice(0, 12) + '…'
  return 'Conversation'
}

function scrollToBottom() {
  if (msgList.value) {
    msgList.value.scrollTop = msgList.value.scrollHeight
  }
}

async function dumpMessages() {
  if (!store.activeConversationId) return
  const msgs = store.messagesByConv[store.activeConversationId] || []
  const lines = msgs.map((m, i) => `${i}: hlc=${m.hlc} sender=${(m.sender||'').slice(0,12)} content="${(m.content||'').slice(0,40)}"`)
  try {
    await window['go']['app']['App']['WriteLogFile']('message_dump.txt', lines.join('\n'))
  } catch(e) { /* ignore */ }
}

async function doSend() {
  const value = text.value.trim()
  if (!value || !activeConv.value) return
  sending.value = true
  errorMsg.value = ''
  try {
    if (activeConv.value.type === 'team') {
      await sendTeamChat(value)
    } else {
      const peer = activeConv.value.peer_pubkey
      if (!peer) throw new Error('Missing peer pubkey for private conversation')
      await sendPrivateChat(peer, value)
    }
    text.value = ''
    await nextTick()
    inputRef.value?.focus()
    scrollToBottom()
  } catch (e) {
    errorMsg.value = String(e)
  } finally {
    sending.value = false
  }
}
</script>

<style scoped>
.chat-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.chat-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--color-divider);
  background: var(--color-surface);
}
.chat-icon { font-size: 14px; }
.chat-title {
  font-family: var(--font-display);
  font-size: 14px;
  font-weight: 600;
  color: var(--color-text);
}
.chat-peer-status {
  margin-left: auto;
  font-size: 11px;
  color: var(--color-text-muted);
}
.message-list {
  flex: 1;
  min-height: 0;
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.load-more-row {
  text-align: center;
  padding: 4px 0 8px;
}
.message-item.self .message-body { color: var(--color-accent); }
.message-item.self .message-sender { color: var(--color-accent); }
.chat-input-area input { background: var(--color-surface); }
</style>
