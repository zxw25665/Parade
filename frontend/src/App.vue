<template>
  <div class="app-layout">
    <!-- ===== Left Panel: Identity, Team, Peers ===== -->
    <aside class="side-panel left" :class="{ open: leftOpen, collapsed: !leftOpen }">
      <button class="panel-toggle" @click="leftOpen = !leftOpen" :title="leftOpen ? 'Collapse left panel' : 'Expand left panel'">
        {{ leftOpen ? '◀' : '▶' }}
      </button>
      <div class="side-panel-inner" v-show="leftOpen">
        <div class="logo">Parade <span style="font-weight:400;font-size:12px;color:var(--color-text-dim)">游行</span></div>

        <CollapsibleSection title="Identity" :default-open="true">
          <IdentityPanel />
        </CollapsibleSection>

        <CollapsibleSection title="Team" :default-open="true">
          <TeamPanel />
        </CollapsibleSection>

        <CollapsibleSection title="Peers" :default-open="true">
          <PeerList />
        </CollapsibleSection>
      </div>
    </aside>

    <!-- ===== Center: Chat ===== -->
    <main class="center-panel">
      <ChatPanel />
    </main>

    <!-- ===== Right Panel: Files, Downloads, History ===== -->
    <aside class="side-panel right" :class="{ open: rightOpen, collapsed: !rightOpen }">
      <button class="panel-toggle" @click="rightOpen = !rightOpen" :title="rightOpen ? 'Collapse right panel' : 'Expand right panel'">
        {{ rightOpen ? '▶' : '◀' }}
      </button>
      <div class="side-panel-inner" v-show="rightOpen">
        <CollapsibleSection title="Files" :default-open="true">
          <FileBrowser />
        </CollapsibleSection>

        <CollapsibleSection title="Downloads" :default-open="true">
          <DownloadList />
        </CollapsibleSection>

        <CollapsibleSection title="History" :default-open="false">
          <HistoryViewer />
        </CollapsibleSection>
      </div>
    </aside>
  </div>
</template>

<script setup>
import { ref, provide, onMounted, onUnmounted } from 'vue'
import { useEvents } from './composables/useEvents.js'
import { useStore } from './composables/useStore.js'
import { OnForeground } from './lib/wailsjs/go/app/App.js'
import IdentityPanel from './components/IdentityPanel.vue'
import TeamPanel from './components/TeamPanel.vue'
import PeerList from './components/PeerList.vue'
import ChatPanel from './components/ChatPanel.vue'
import FileBrowser from './components/FileBrowser.vue'
import DownloadList from './components/DownloadList.vue'
import HistoryViewer from './components/HistoryViewer.vue'
import CollapsibleSection from './components/CollapsibleSection.vue'

const eventsState = useEvents()
const store = useStore()
provide('events', eventsState)
provide('store', store)

const leftOpen = ref(true)
const rightOpen = ref(true)

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
/* All layout styles are in index.html global CSS */
/* Scoped blocks only for App-specific overrides if needed */
</style>
