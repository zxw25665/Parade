<template>
  <div class="app-layout">
    <nav class="sidebar">
      <div class="logo">Parade (游行)</div>
      <button v-for="tab in tabs" :key="tab.id"
        :class="{ active: currentTab === tab.id }"
        @click="currentTab = tab.id">
        {{ tab.label }}
      </button>
    </nav>
    <main class="content">
      <component :is="currentComponent" />
    </main>
  </div>
</template>

<script setup>
import { ref, computed, provide, onMounted, onUnmounted } from 'vue'
import { useEvents } from './composables/useEvents.js'
import { useStore } from './composables/useStore.js'
import { OnForeground } from './lib/wailsjs/go/app/App.js'
import IdentityPanel from './components/IdentityPanel.vue'
import TeamPanel from './components/TeamPanel.vue'
import PeerList from './components/PeerList.vue'
import TeamChat from './components/TeamChat.vue'
import PrivateChat from './components/PrivateChat.vue'
import FileBrowser from './components/FileBrowser.vue'
import DownloadList from './components/DownloadList.vue'
import HistoryViewer from './components/HistoryViewer.vue'

const eventsState = useEvents()
const store = useStore()
provide('events', eventsState)
provide('store', store)

const currentTab = ref('identity')

const tabs = [
  { id: 'identity', label: 'Identity' },
  { id: 'team', label: 'Team' },
  { id: 'peers', label: 'Peers' },
  { id: 'teamchat', label: 'Team Chat' },
  { id: 'privatechat', label: 'Private Chat' },
  { id: 'files', label: 'Files' },
  { id: 'downloads', label: 'Downloads' },
  { id: 'history', label: 'History' }
]

const currentComponent = computed(() => {
  const map = {
    identity: IdentityPanel,
    team: TeamPanel,
    peers: PeerList,
    teamchat: TeamChat,
    privatechat: PrivateChat,
    files: FileBrowser,
    downloads: DownloadList,
    history: HistoryViewer
  }
  return map[currentTab.value]
})

function onVisibilityChange() {
  if (document.visibilityState === 'visible') {
    OnForeground().catch(() => {})
  }
}

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
})

onUnmounted(() => {
  document.removeEventListener('visibilitychange', onVisibilityChange)
})
</script>

<style scoped>
.app-layout { display: flex; width: 100%; height: 100%; }
.sidebar {
  width: 160px; min-width: 160px; background: #16213e;
  display: flex; flex-direction: column; padding: 12px 0;
}
.logo { padding: 12px 16px; font-size: 13px; font-weight: bold; color: #e94560; border-bottom: 1px solid #0f3460; margin-bottom: 8px; }
.sidebar button {
  background: transparent; border: none; text-align: left;
  padding: 8px 16px; font-size: 13px; color: #8a8aaf; border-radius: 0; cursor: pointer;
}
.sidebar button:hover { background: #0f3460; color: #e0e0e0; }
.sidebar button.active { background: #0f3460; color: #e94560; border-left: 3px solid #e94560; padding-left: 13px; }
.content { flex: 1; overflow-y: auto; padding: 16px; }
</style>
