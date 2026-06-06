<template>
    <div class="row">
      <select v-model="targetPeer" style="flex:1">
        <option value="">{{ $t('chat.selectPeer') }}</option>
        <option v-for="p in peers" :key="p.pubkey" :value="p.pubkey">
          {{ p.ip }} ({{ p.pubkey.slice(0, 16) }}...)
        </option>
      </select>
    </div>
    <div class="list">
      <div v-if="messages.length === 0" style="font-size:13px;color:#8a8aaf">{{ $t('chat.noMessages') }}</div>
      <div class="message-item" v-for="msg in messages" :key="msg.id" :style="{ color: msg.direction === 'send' ? '#4ecca3' : '#e0e0e0' }">
        <div>
          <span class="message-sender">{{ msg.senderId ? (msg.senderId.length > 12 ? msg.senderId.slice(0, 8) + '...' : msg.senderId) : $t('chat.me') }}</span>
          <span class="message-meta">{{ msg.hlc }}</span>
        </div>
        <div class="message-body">{{ msg.content }}</div>
      </div>
    </div>
    <div class="row" style="margin-top:12px">
      <input v-model="text" :placeholder="$t('chat.typePrivate')" @keyup.enter="doSend" style="flex:1" />
      <button @click="doSend" :disabled="!text || !targetPeer || loading">{{ $t('chat.send') }}</button>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
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
    const content = text.value
    await sendPrivateChat(targetPeer.value, content)

    state.privateMessages.unshift({
      id: Date.now().toString(),
      hlc: new Date().toISOString(),
      senderId: '',
      receiverId: targetPeer.value,
      content: content,
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
