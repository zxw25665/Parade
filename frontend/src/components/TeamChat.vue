<template>

    <div class="list" ref="msgList">
      <div v-if="state.teamMessages.length === 0" style="font-size:13px;color:#8a8aaf">{{ $t('chat.noMessages') }}</div>
      <div class="message-item" v-for="msg in state.teamMessages" :key="msg.id" :style="{ color: msg.direction === 'send' ? '#4ecca3' : '#e0e0e0' }">
        <div>
          <span class="message-sender">{{ msg.sender && msg.sender.length > 12 ? msg.sender.slice(0, 8) + '...' : msg.sender }}</span>
          <span class="message-meta">{{ msg.hlc }}</span>
        </div>
        <div class="message-body">{{ msg.content }}</div>
      </div>
    </div>

    <div class="row" style="margin-top:12px">
      <input v-model="text" :placeholder="$t('chat.typeMessage')" @keyup.enter="doSend" style="flex:1" />
      <button @click="doSend" :disabled="!text.trim() || loading">{{ $t('chat.send') }}</button>
    </div>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
</template>

<script setup>
import { ref, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'

const { t } = useI18n()
const { sendTeamChat } = useBackend()

const state = inject('events')
const store = inject('store')

const text = ref('')
const loading = ref(false)
const errorMsg = ref('')

async function doSend() {
  if (!text.value.trim()) return
  loading.value = true
  errorMsg.value = ''
  const content = text.value.trim()

  try {
    await sendTeamChat(content)

    state.teamMessages.unshift({
      id: Date.now().toString(),
      hlc: new Date().toISOString(),
      sender: 'me',
      content: content,
      direction: 'send',
      teamId: store.activeTeamId
    })

    text.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
