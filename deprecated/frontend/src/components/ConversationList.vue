<template>
  <div class="conv-list-root">
    <div v-if="!store.teamJoined" class="hint">
      {{ $t('team.mustLogin') }}
    </div>
    <div v-else class="list conv-list">
      <div
        v-for="c in sortedConversations"
        :key="c.id"
        class="list-item conv-item"
        :class="{ active: c.id === store.activeConversationId }"
        @click="select(c.id)"
      >
        <span class="status-dot" :class="convStatusClass(c)"></span>
        <span class="conv-icon" :class="c.type === 'team' ? 'team' : 'private'">
          {{ c.type === 'team' ? '👥' : '👤' }}
        </span>
        <div class="conv-body">
          <div class="conv-name">{{ convDisplay(c) }}</div>
          <div class="conv-preview">{{ c.last_message || $t('conv.noMessagesYet') }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, inject, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'
import { EventsOn, EventsOff } from '../lib/wailsjs/runtime/runtime'

const { t } = useI18n()
const store = inject('store')
const { listConversations } = useBackend()

const conversations = computed(() => store.conversations || [])

const sortedConversations = computed(() => {
  const seen = new Set()
  const list = [...conversations.value].filter(c => {
    if (seen.has(c.id)) return false
    seen.add(c.id)
    return true
  })
  list.sort((a, b) => {
    if (a.type === 'team' && b.type !== 'team') return -1
    if (a.type !== 'team' && b.type === 'team') return 1
    return 0
  })
  return list
})

async function refresh() {
  if (!store.loggedIn || !store.teamJoined) return
  try {
    const result = await listConversations()
    store.conversations = Array.isArray(result) ? result : []
  } catch (e) { /* ignore */ }
}

onMounted(() => {
  refresh()
  EventsOn('ui_conversation_updated', refresh)
})
onUnmounted(() => {
  EventsOff('ui_conversation_updated')
})
watch(() => store.loggedIn, refresh)
watch(() => store.teamJoined, refresh)
watch(() => store.activeTeamId, refresh)

function truncate(s, n) {
  if (!s) return ''
  return s.length > n ? s.slice(0, n) + '…' : s
}

function convDisplay(c) {
  if (c.type === 'team') return c.display_name || t('chat.teamChat')
  if (c.peer_pubkey) return c.display_name || truncate(c.peer_pubkey, 16)
  return c.display_name || t('chat.privateChat')
}

function convStatusClass(c) {
  if (c.type === 'team') return 'team-dot'
  const p = (store.peersWithStatus || []).find(x => x.pubkey === c.peer_pubkey)
  return p && p.status === 'online' ? 'online' : 'offline'
}

function select(id) {
  store.activeConversationId = id
}
</script>

<style scoped>
.conv-list-root {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.conv-list {
  max-height: 200px;
  overflow-y: auto;
}
.conv-item {
  cursor: pointer;
  gap: 8px;
  padding: 6px 8px;
  align-items: center;
}
.conv-item.active {
  background: var(--color-accent-muted);
  border-left: 3px solid var(--color-accent);
  padding-left: 5px;
}
.conv-icon {
  font-size: 14px;
  width: 20px;
  text-align: center;
  flex-shrink: 0;
}
.conv-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.conv-name {
  font-size: 12px;
  font-weight: 500;
  color: var(--color-text);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.conv-preview {
  font-size: 10px;
  color: var(--color-text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.status-dot.online { background: var(--color-success); }
.status-dot.offline { background: var(--color-danger); }
.status-dot.team-dot { background: var(--color-accent); }
</style>
