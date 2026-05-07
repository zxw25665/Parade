<template>
  <div class="panel">
    <h2>Team Chat</h2>

    <div class="row" style="margin-bottom: 12px;">
      <select v-model="activeChannelId" style="flex: 1;">
        <option value="">Team (General)</option>
        <option v-for="ch in store.channels" :key="ch.id" :value="ch.id">{{ ch.name }}</option>
      </select>
    </div>

    <div class="row" style="margin-bottom: 12px;">
      <input v-model="newChannelName" placeholder="New channel name..." style="flex: 1;" />
      <button @click="doCreateChannel" :disabled="!newChannelName.trim() || creatingChannel">Create</button>
    </div>

    <div class="list" ref="msgList">
      <div v-if="filteredMessages.length === 0" style="font-size:13px;color:#8a8aaf">No messages yet</div>
      <div class="list-item" v-for="msg in filteredMessages" :key="msg.id" :style="{ color: msg.direction === 'send' ? '#4ecca3' : '#e0e0e0' }">
        <div style="font-size:11px;color:#8a8aaf">{{ msg.sender }} · {{ msg.hlc }}</div>
        <div>{{ msg.content }}</div>
      </div>
    </div>

    <div class="row" style="margin-top:12px">
      <input v-model="text" placeholder="Type a message..." @keyup.enter="doSend" style="flex:1" />
      <button @click="doSend" :disabled="!text.trim() || loading">Send</button>
    </div>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
  </div>
</template>

<script setup>
import { ref, computed, inject, onMounted, watch } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const {
  sendTeamChat,
  sendChannelChat,
  createChannel,
  listChannels
} = useBackend()

const state = inject('events')
const store = inject('store')

const activeChannelId = computed({
  get: () => store.activeChannelId,
  set: (val) => { store.activeChannelId = val }
})

const filteredMessages = computed(() => {
  if (!activeChannelId.value) {
    return state.teamMessages.filter(msg => !msg.channelId)
  }
  return state.teamMessages.filter(msg => msg.channelId === activeChannelId.value)
})

const text = ref('')
const newChannelName = ref('')
const loading = ref(false)
const creatingChannel = ref(false)
const errorMsg = ref('')

async function loadChannels() {
  if (!store.activeTeamId) return
  try {
    const channels = await listChannels()
    store.channels = channels || []
  } catch (e) {
    console.error('Failed to load channels:', e)
  }
}

async function doCreateChannel() {
  if (!newChannelName.value.trim()) return
  creatingChannel.value = true
  errorMsg.value = ''
  try {
    await createChannel(newChannelName.value.trim())
    newChannelName.value = ''
    await loadChannels()
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    creatingChannel.value = false
  }
}

async function doSend() {
  if (!text.value.trim()) return
  loading.value = true
  errorMsg.value = ''
  const content = text.value.trim()
  const channelId = activeChannelId.value

  try {
    if (channelId) {
      await sendChannelChat(channelId, content)
    } else {
      await sendTeamChat(content)
    }

    state.teamMessages.unshift({
      id: Date.now().toString(),
      hlc: new Date().toISOString(),
      sender: 'me',
      content: content,
      direction: 'send',
      channelId: channelId,
      teamId: store.activeTeamId
    })

    text.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadChannels()
})

watch(() => store.activeTeamId, () => {
  loadChannels()
})
</script>
