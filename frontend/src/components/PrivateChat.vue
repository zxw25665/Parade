<template>
  <div class="panel">
    <h2>Private Chat</h2>
    <div class="row">
      <select v-model="targetPeer" style="flex:1">
        <option value="">-- Select peer --</option>
        <option v-for="p in peers" :key="p.pubkey" :value="p.pubkey">
          {{ p.ip }} ({{ p.pubkey.slice(0, 16) }}...)
        </option>
      </select>
    </div>
    <div class="list" style="max-height:250px">
      <div v-if="messages.length === 0" style="font-size:13px;color:#8a8aaf">No messages yet</div>
      <div class="list-item" v-for="msg in messages" :key="msg.id" :style="{ color: msg.direction === 'send' ? '#4ecca3' : '#e0e0e0' }">
        <div style="font-size:11px;color:#8a8aaf">{{ msg.senderId || 'me' }} · {{ msg.hlc }}</div>
        <div>{{ msg.content }}</div>
      </div>
    </div>
    <div class="row" style="margin-top:12px">
      <input v-model="text" placeholder="Type a private message..." @keyup.enter="doSend" style="flex:1" />
      <button @click="doSend" :disabled="!text || !targetPeer || loading">Send</button>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { sendPrivateChat } = useBackend()
const state = inject('events')
const peers = computed(() => state.peers)

const targetPeer = ref('')
const text = ref('')
const loading = ref(false)
const errorMsg = ref('')

const messages = computed(() => {
  const selected = targetPeer.value
  if (!selected) return []
  const myPubkey = ''
  return state.privateMessages.filter(
    m => m.senderId === selected || m.receiverId === selected
  )
})

async function doSend() {
  if (!text.value.trim() || !targetPeer.value) return
  loading.value = true; errorMsg.value = ''
  try {
    await sendPrivateChat(targetPeer.value, text.value)
    text.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
