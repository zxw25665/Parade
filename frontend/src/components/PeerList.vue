<template>

    <!-- Manual connect -->
    <div class="row">
      <input v-model="connectIP" :placeholder="$t('peer.connectPlaceholder')" @keyup.enter="doConnect" style="flex:1" />
      <button @click="doConnect" :disabled="!connectIP || connecting">{{ $t('peer.connect') }}</button>
      <button @click="doRefresh" :disabled="connecting">{{ $t('peer.refresh') }}</button>
      <span style="font-size:12px">{{ $t('peer.peerCount', { count: peers.length }) }}</span>
    </div>
    <div v-if="connectError" class="error">{{ connectError }}</div>

    <!-- Peer list -->
    <div class="list" v-if="peers.length || Object.keys(store.peerTests).length">
      <div v-for="p in allPeers" :key="p.key" style="border-bottom:1px solid #16213e;padding:6px 0">
        <div class="list-item" style="cursor:pointer" @click="toggleExpanded(p)">
          <span class="badge" :class="p.source === 'mDNS' ? 'badge-green' : ''">
            {{ p.ip }}
          </span>
          <span style="font-size:11px;word-break:break-all;margin-left:8px">
            {{ p.shortPubkey }}
          </span>
          <span v-if="p.source === 'mDNS'" style="font-size:10px;color:#8a8aaf;margin-left:8px">{{ $t('peer.mdns') }}</span>
        </div>

        <!-- Test result panel -->
        <div v-if="p.expanded && p.test" style="padding:6px 8px;background:#0f3460;border-radius:4px;margin-top:4px;font-size:12px">
          <div v-for="phase in phases(p.test)" :key="phase.key" style="margin-bottom:2px">
            <span :style="{color: phase.success ? '#4ecca3' : '#e94560'}">
              {{ phase.success ? '✓' : '✗' }}
            </span>
            <span style="margin-left:4px;color:#ccc">{{ phase.label }}</span>
            <span v-if="phase.error" style="color:#e94560;margin-left:8px">{{ phase.error }}</span>
          </div>
        </div>
        <div v-else-if="p.expanded" style="font-size:11px;color:#8a8aaf;padding:6px 8px">
          {{ $t('peer.notTested') }}
        </div>
      </div>
    </div>
    <div v-else style="font-size:13px;color:#8a8aaf;margin-top:8px">
      {{ $t('peer.noPeers') }}
    </div>
</template>

<script setup>
import { ref, computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'
import { useStore } from '../composables/useStore.js'

const { t } = useI18n()
const { getPeers, connectToPeer } = useBackend()
const eventsState = inject('events')
const store = useStore()
const peers = computed(() => eventsState.peers)

const connectIP = ref('')
const connecting = ref(false)
const connectError = ref('')

// Merge mDNS peers with manually connected peers
const allPeers = computed(() => {
  const map = {}

  // mDNS peers
  for (const p of peers.value) {
    const key = p.ip || p.pubkey
    if (!map[key]) {
      map[key] = {
        key,
        ip: p.ip,
        pubkey: p.pubkey,
        shortPubkey: (p.pubkey || '').slice(0, 16) + '...',
        source: 'mDNS',
        test: null,
        expanded: false
      }
    }
  }

  // Manual peers with test results
  for (const [ip, t] of Object.entries(store.peerTests)) {
    if (map[ip]) {
      map[ip].test = t
    } else {
      map[ip] = {
        key: ip,
        ip: t.ip || ip,
        pubkey: t.pubkey || '',
        shortPubkey: (t.pubkey || 'unknown').slice(0, 16) + '...',
        source: 'manual',
        test: t,
        expanded: true
      }
    }
  }

  return Object.values(map)
})

function toggleExpanded(p) {
  p.expanded = !p.expanded
}

function phases(test) {
  return [
    { key: 'phase1', success: test.phase1?.success, label: test.phase1?.label || t('peer.phaseNormal'), error: test.phase1?.error || '' },
    { key: 'phase2', success: test.phase2?.success, label: test.phase2?.label || t('peer.phaseSameTeam'), error: test.phase2?.error || '' },
    { key: 'phase3Send', success: test.phase3Send?.success, label: test.phase3Send?.label || t('peer.phaseMsgSent'), error: test.phase3Send?.error || '' },
    { key: 'phase3Recv', success: test.phase3Recv?.success, label: test.phase3Recv?.label || t('peer.phaseMsgReceived'), error: test.phase3Recv?.error || '' }
  ]
}

async function doConnect() {
  const ip = connectIP.value.trim()
  if (!ip) return
  connecting.value = true
  connectError.value = ''
  try {
    const result = await connectToPeer(ip)
    store.peerTests[ip] = result
  } catch (e) {
    connectError.value = t('peer.connectFailed') + ': ' + e.toString()
  } finally {
    connecting.value = false
  }
}

async function doRefresh() {
  try {
    const result = await getPeers()
    if (Array.isArray(result)) {
      eventsState.peers = result.map(p => ({
        pubkey: p.pubKey || p.pubkey || p.PubKeyBase64,
        ip: p.ip || p.IPAddress
      }))
    }
  } catch (e) {
    connectError.value = t('peer.refreshFailed') + ': ' + e.toString()
  }
}
</script>
