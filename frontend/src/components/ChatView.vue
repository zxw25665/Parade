<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useChatStore } from '@/stores/chat'
import { usePeersStore } from '@/stores/peers'
import { useAuthStore } from '@/stores/auth'
import ConversationList from './ConversationList.vue'
import ChatPanel from './ChatPanel.vue'
import PeerList from './PeerList.vue'
import FileBrowser from './FileBrowser.vue'
import DownloadList from './DownloadList.vue'

const chatStore = useChatStore()
const peersStore = usePeersStore()
const authStore = useAuthStore()

// UI State
const leftSidebarCollapsed = ref(false)
const rightPanelCollapsed = ref(true)
const activeRightTab = ref<'files' | 'downloads'>('files')
const isMobile = ref(false)

// Resizable panel state
const leftSidebarWidth = ref(280)
const rightPanelWidth = ref(320)
const isResizingLeft = ref(false)
const isResizingRight = ref(false)

const resizeHandler = () => {
  isMobile.value = window.innerWidth < 768
  if (isMobile.value) {
    leftSidebarCollapsed.value = true
    rightPanelCollapsed.value = true
  }
}

onMounted(async () => {
  resizeHandler()
  window.addEventListener('resize', resizeHandler)

  // Load initial data
  if (authStore.isAuthenticated) {
    await loadData()
  }
})

onUnmounted(() => {
  window.removeEventListener('resize', resizeHandler)
})

// Watch for auth changes
watch(() => authStore.isAuthenticated, async (isAuth) => {
  if (isAuth) {
    await loadData()
  }
})

async function loadData() {
  await Promise.all([
    chatStore.loadConversations(),
    peersStore.loadPeers(),
  ])
}

// Resizer handlers
function startResizeLeft(e: MouseEvent) {
  isResizingLeft.value = true
  document.addEventListener('mousemove', handleResizeLeft)
  document.addEventListener('mouseup', stopResizeLeft)
}

function handleResizeLeft(e: MouseEvent) {
  if (!isResizingLeft.value) return
  const newWidth = Math.max(200, Math.min(400, e.clientX))
  leftSidebarWidth.value = newWidth
}

function stopResizeLeft() {
  isResizingLeft.value = false
  document.removeEventListener('mousemove', handleResizeLeft)
  document.removeEventListener('mouseup', stopResizeLeft)
}

function startResizeRight(e: MouseEvent) {
  isResizingRight.value = true
  document.addEventListener('mousemove', handleResizeRight)
  document.addEventListener('mouseup', stopResizeRight)
}

function handleResizeRight(e: MouseEvent) {
  if (!isResizingRight.value) return
  const newWidth = Math.max(250, Math.min(500, window.innerWidth - e.clientX))
  rightPanelWidth.value = newWidth
}

function stopResizeRight() {
  isResizingRight.value = false
  document.removeEventListener('mousemove', handleResizeRight)
  document.removeEventListener('mouseup', stopResizeRight)
}

function toggleLeftSidebar() {
  leftSidebarCollapsed.value = !leftSidebarCollapsed.value
}

function toggleRightPanel() {
  rightPanelCollapsed.value = !rightPanelCollapsed.value
}

async function handleConnectPeer(ipAddress: string) {
  try {
    await peersStore.connectToPeer(ipAddress)
  } catch (error) {
    console.error('Failed to connect:', error)
  }
}
</script>

<template>
  <div class="chat-view" :class="{ 'sidebar-collapsed': leftSidebarCollapsed, 'right-collapsed': rightPanelCollapsed }">
    <!-- Left Sidebar: Conversations + Peers -->
    <aside 
      class="left-sidebar"
      :style="{ width: leftSidebarCollapsed ? '0px' : `${leftSidebarWidth}px` }"
    >
      <div class="sidebar-content">
        <div class="sidebar-section">
          <div class="section-header">
            <h3 class="section-title">Conversations</h3>
            <button 
              class="btn btn-ghost btn-icon" 
              @click="loadData"
              title="Refresh"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M23 4v6h-6M1 20v-6h6"/>
                <path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15"/>
              </svg>
            </button>
          </div>
          <ConversationList />
        </div>
        
        <div class="sidebar-divider"></div>
        
        <div class="sidebar-section peers-section">
          <div class="section-header">
            <h3 class="section-title">Peers</h3>
            <span class="peer-count">{{ peersStore.onlinePeerCount }} online</span>
          </div>
          <PeerList @connect="handleConnectPeer" />
        </div>
      </div>
    </aside>

    <!-- Left Resizer -->
    <div 
      v-if="!leftSidebarCollapsed" 
      class="resizer resizer-left"
      @mousedown="startResizeLeft"
    ></div>

    <!-- Center: Chat Panel -->
    <main class="chat-main">
      <ChatPanel />
    </main>

    <!-- Right Resizer -->
    <div 
      v-if="!rightPanelCollapsed" 
      class="resizer resizer-right"
      @mousedown="startResizeRight"
    ></div>

    <!-- Right Panel: Files/Downloads -->
    <aside 
      class="right-panel"
      :style="{ width: rightPanelCollapsed ? '0px' : `${rightPanelWidth}px` }"
    >
      <div class="panel-header">
        <div class="panel-tabs">
          <button 
            class="panel-tab"
            :class="{ active: activeRightTab === 'files' }"
            @click="activeRightTab = 'files'"
          >
            Files
          </button>
          <button 
            class="panel-tab"
            :class="{ active: activeRightTab === 'downloads' }"
            @click="activeRightTab = 'downloads'"
          >
            Downloads
          </button>
        </div>
      </div>
      
      <div class="panel-content">
        <FileBrowser v-if="activeRightTab === 'files'" />
        <DownloadList v-else />
      </div>
    </aside>

    <!-- Mobile Toggle Buttons -->
    <div class="mobile-toggles">
      <button 
        class="mobile-toggle"
        :class="{ active: !leftSidebarCollapsed }"
        @click="toggleLeftSidebar"
        title="Toggle conversations"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2"/>
          <circle cx="9" cy="7" r="4"/>
          <path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/>
        </svg>
      </button>
      <button 
        class="mobile-toggle"
        :class="{ active: !rightPanelCollapsed }"
        @click="toggleRightPanel"
        title="Toggle files"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.chat-view {
  display: flex;
  width: 100%;
  height: 100%;
  background: var(--bg-base);
  position: relative;
  overflow: hidden;
}

/* Left Sidebar */
.left-sidebar {
  flex-shrink: 0;
  background: var(--bg-surface);
  border-right: 1px solid var(--border-default);
  overflow: hidden;
  transition: width var(--transition-base);
}

.sidebar-content {
  display: flex;
  flex-direction: column;
  height: 100%;
  width: 280px;
}

.sidebar-section {
  display: flex;
  flex-direction: column;
  padding: var(--space-3);
}

.peers-section {
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

.section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.section-title {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.peer-count {
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.sidebar-divider {
  height: 1px;
  background: var(--border-default);
  margin: 0 var(--space-3);
}

/* Main Chat Area */
.chat-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  background: var(--bg-base);
}

/* Right Panel */
.right-panel {
  flex-shrink: 0;
  background: var(--bg-surface);
  border-left: 1px solid var(--border-default);
  display: flex;
  flex-direction: column;
  overflow: hidden;
  transition: width var(--transition-base);
}

.panel-header {
  padding: var(--space-3);
  border-bottom: 1px solid var(--border-default);
}

.panel-tabs {
  display: flex;
  gap: var(--space-2);
}

.panel-tab {
  flex: 1;
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-muted);
  background: transparent;
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.panel-tab:hover {
  color: var(--text-primary);
  background: var(--bg-elevated);
}

.panel-tab.active {
  color: var(--primary);
  background: var(--primary-glow);
}

.panel-content {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-6);
}

.files-placeholder,
.downloads-placeholder {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
  color: var(--text-muted);
  text-align: center;
}

.files-placeholder svg,
.downloads-placeholder svg {
  opacity: 0.5;
}

.files-placeholder p,
.downloads-placeholder p {
  font-size: var(--text-sm);
}

/* Resizers */
.resizer {
  width: 4px;
  cursor: col-resize;
  background: transparent;
  transition: background var(--transition-fast);
  z-index: 10;
}

.resizer:hover,
.resizer-left:active,
.resizer-right:active {
  background: var(--primary);
}

/* Mobile Toggles */
.mobile-toggles {
  display: none;
  position: fixed;
  bottom: var(--space-4);
  left: 50%;
  transform: translateX(-50%);
  gap: var(--space-3);
  z-index: 100;
}

.mobile-toggle {
  width: 44px;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-full);
  color: var(--text-secondary);
  transition: all var(--transition-fast);
  box-shadow: var(--shadow-lg);
}

.mobile-toggle:hover,
.mobile-toggle.active {
  background: var(--primary);
  border-color: var(--primary);
  color: white;
}

/* Responsive */
@media (max-width: 768px) {
  .mobile-toggles {
    display: flex;
  }
  
  .left-sidebar {
    position: fixed;
    left: 0;
    top: 0;
    bottom: 0;
    z-index: 50;
    box-shadow: var(--shadow-lg);
  }
  
  .right-panel {
    position: fixed;
    right: 0;
    top: 0;
    bottom: 0;
    z-index: 50;
    box-shadow: var(--shadow-lg);
  }
  
  .resizer {
    display: none;
  }
}
</style>
