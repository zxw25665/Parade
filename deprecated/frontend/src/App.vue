<template>
  <div class="app-layout">
    <!-- ===== Left Panel: Identity, Conversations, Peers ===== -->
    <aside class="side-panel left" :class="{ open: leftOpen, collapsed: !leftOpen }">
      <button class="panel-toggle" @click="leftOpen = !leftOpen" :title="leftOpen ? $t('panel.collapseLeft') : $t('panel.expandLeft')">
        {{ leftOpen ? '◀' : '▶' }}
      </button>
      <div class="side-panel-inner" v-show="leftOpen">
        <div class="logo-header">
          <div class="logo">{{ $t('app.title') }} <span style="font-weight:400;font-size:12px;color:var(--color-text-dim)">{{ $t('app.subtitle') }}</span></div>
          <LanguageToggle />
        </div>

        <CollapsibleSection :title="$t('panel.identity')" :default-open="true">
          <IdentityPanel />
        </CollapsibleSection>

        <CollapsibleSection :title="$t('panel.team')" :default-open="true">
          <TeamPanel />
        </CollapsibleSection>

        <CollapsibleSection :title="$t('panel.conversations')" :default-open="true">
          <ConversationList />
        </CollapsibleSection>

        <CollapsibleSection :title="$t('panel.peers')" :default-open="true">
          <PeerStatus />
        </CollapsibleSection>
      </div>
    </aside>

    <!-- ===== Center: Chat (conversation-bound, no tabs) ===== -->
    <main class="center-panel">
      <ChatPanel />
    </main>

    <!-- ===== Right Panel: Files, Downloads, Logs ===== -->
    <aside class="side-panel right" :class="{ open: rightOpen, collapsed: !rightOpen }">
      <button class="panel-toggle" @click="rightOpen = !rightOpen" :title="rightOpen ? $t('panel.collapseRight') : $t('panel.expandRight')">
        {{ rightOpen ? '▶' : '◀' }}
      </button>
      <div class="side-panel-inner" v-show="rightOpen">
        <CollapsibleSection :title="$t('panel.files')" :default-open="true">
          <FileBrowser />
        </CollapsibleSection>

        <CollapsibleSection :title="$t('panel.downloads')" :default-open="true">
          <DownloadList />
        </CollapsibleSection>

        <CollapsibleSection :title="$t('panel.logs')" :default-open="false">
          <LogPanel />
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
import ConversationList from './components/ConversationList.vue'
import PeerStatus from './components/PeerStatus.vue'
import ChatPanel from './components/ChatPanel.vue'
import FileBrowser from './components/FileBrowser.vue'
import DownloadList from './components/DownloadList.vue'
import LogPanel from './components/LogPanel.vue'
import CollapsibleSection from './components/CollapsibleSection.vue'
import LanguageToggle from './components/LanguageToggle.vue'

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
.logo-header {
  display: flex;
  align-items: center;
  padding-right: 4px;
}
</style>
