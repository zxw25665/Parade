<template>
  <div class="panel">
    <h2>Team Chat</h2>
    <div class="list" ref="msgList">
      <div v-if="messages.length === 0" style="font-size:13px;color:#8a8aaf">No messages yet</div>
      <div class="list-item" v-for="msg in messages" :key="msg.id" :style="{ color: msg.direction === 'send' ? '#4ecca3' : '#e0e0e0' }">
        <div style="font-size:11px;color:#8a8aaf">{{ msg.sender }} · {{ msg.hlc }}</div>
        <div>{{ msg.content }}</div>
      </div>
    </div>
    <div class="row" style="margin-top:12px">
      <input v-model="text" placeholder="Type a team message..." @keyup.enter="doSend" style="flex:1" />
      <button @click="doSend" :disabled="!text || loading">Send</button>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { sendTeamChat } = useBackend()
const state = inject('events')
const messages = computed(() => state.teamMessages)

const text = ref('')
const loading = ref(false)
const errorMsg = ref('')

async function doSend() {
  if (!text.value.trim()) return
  loading.value = true; errorMsg.value = ''
  try {
    await sendTeamChat(text.value)
    state.teamMessages.unshift({
      id: Date.now().toString(),
      hlc: new Date().toISOString(),
      sender: 'me',
      content: text.value,
      direction: 'send'
    })
    text.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
