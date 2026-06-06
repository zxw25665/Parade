<template>
    <div class="row">
      <label style="font-size:13px">{{ $t('history.limit') }}:</label>
      <input v-model.number="limit" type="number" min="1" max="200" style="width:80px" />
      <label style="font-size:13px">{{ $t('history.offset') }}:</label>
      <input v-model.number="offset" type="number" min="0" style="width:80px" />
      <button @click="doFetch" :disabled="loading">{{ $t('history.fetch') }}</button>
    </div>
    <div class="list">
      <div v-if="messages.length === 0 && !loading" style="font-size:13px;color:#8a8aaf">{{ $t('history.noMessages') }}</div>
      <div class="list-item" v-for="msg in messages" :key="msg.id">
        <div style="display:flex;gap:8px;font-size:11px;color:#8a8aaf;margin-bottom:2px">
          <span>{{ msg.sender }}</span>
          <span>{{ msg.hlc }}</span>
        </div>
        <div style="font-size:13px">{{ msg.content }}</div>
      </div>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
</template>

<script setup>
import { ref } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { getRecentHistory } = useBackend()

const limit = ref(20)
const offset = ref(0)
const loading = ref(false)
const errorMsg = ref('')
const messages = ref([])

async function doFetch() {
  loading.value = true; errorMsg.value = ''
  try {
    const result = await getRecentHistory(limit.value, offset.value)
    messages.value = Array.isArray(result) ? result : []
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
