<template>
  <div class="peer-status-root">
    <div class="row" style="margin-bottom: 6px">
      <input v-model="ipInput" placeholder="192.168.x.x" style="flex:1" @keyup.enter="doConnect" />
      <button class="btn-sm" @click="doConnect" :disabled="!ipInput.trim() || connecting">{{ $t('peer.connect') }}</button>
    </div>
    <div class="row" style="margin-bottom: 6px">
      <button class="btn-sm" style="flex:1" @click="refresh" :disabled="loading">
        {{ $t('peer.refresh') }}
      </button>
    </div>
    <div v-if="peers.length === 0" class="hint">
      {{ $t('peer.noPeers') }}
    </div>
    <div v-else class="list peer-list">
      <div
        v-for="p in peers"
        :key="p.pubkey"
        class="list-item peer-row"
        :class="{ offline: p.status !== 'online' }"
        @click="doStartPm(p.pubkey)"
        :title="$t('peer.clickToChat')"
      >
        <span class="status-emoji">{{ p.status === 'online' ? '🟢' : '🔴' }}</span>
        <div class="peer-body">
          <div class="peer-ip">{{ p.ip || '—' }}</div>
          <div class="peer-pubkey">{{ truncate(p.pubkey, 16) }}</div>
        </div>
      </div>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
  </div>
</template>

<script setup>
import { ref, computed, inject, onMounted } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const store = inject('store')
const { getPeersWithStatus, startPrivateConversation, connectToPeer } = useBackend()

const loading = ref(false)
const errorMsg = ref('')
const ipInput = ref('')
const connecting = ref(false)

const peers = computed(() => store.peersWithStatus || [])

async function refresh() {
  loading.value = true
  errorMsg.value = ''
  try {
    const result = await getPeersWithStatus()
    const list = Array.isArray(result) ? result : []
    store.peersWithStatus = list.map(p => ({
      pubkey: p.pubkey || p.PubKeyBase64,
      ip: p.ip || p.IPAddress || '',
      status: (p.status || '').toLowerCase() === 'online' ? 'online' : 'offline',
      last_heartbeat: p.last_heartbeat || p.LastHeartbeat || '',
      last_online: p.last_online || p.LastOnlineAt || ''
    }))
  } catch (e) {
    errorMsg.value = String(e)
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await refresh()
})

async function doConnect() {
  const ip = ipInput.value.trim()
  if (!ip) return
  connecting.value = true
  errorMsg.value = ''
  try {
    await connectToPeer(ip)
    ipInput.value = ''
    await refresh()
  } catch (e) {
    errorMsg.value = String(e)
  } finally {
    connecting.value = false
  }
}

function truncate(s, n) {
  if (!s) return ''
  return s.length > n ? s.slice(0, n) + '…' : s
}

async function doStartPm(pubkey) {
  if (!pubkey) return
  try {
    const convId = await startPrivateConversation(pubkey)
    store.activeConversationId = convId || ''
  } catch (e) {
    errorMsg.value = String(e)
  }
}
</script>

<style scoped>
.peer-status-root {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.peer-list {
  max-height: 40vh;
}
.peer-row {
  cursor: pointer;
  gap: 10px;
  padding: 6px 10px;
  align-items: center;
}
.peer-row.offline {
  opacity: 0.6;
}
.status-emoji {
  font-size: 12px;
  flex-shrink: 0;
}
.peer-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}
.peer-ip {
  font-size: 12px;
  color: var(--color-text);
  font-weight: 500;
}
.peer-pubkey {
  font-size: 10px;
  color: var(--color-text-dim);
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
</style>
