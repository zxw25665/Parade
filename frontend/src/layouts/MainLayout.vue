<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { usePeersStore } from '@/stores/peers'
import Spinner from '@/components/ui/Spinner.vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const peersStore = usePeersStore()

const sidebarCollapsed = ref(false)

const navItems = [
  { path: '/chat', name: 'Chat', icon: '💬' },
  { path: '/files', name: 'Files', icon: '📁' },
  { path: '/team', name: 'Team', icon: '👥' },
  { path: '/settings', name: 'Settings', icon: '⚙️' },
]

const isActive = (path: string) => route.path === path

async function handleLogout() {
  await authStore.logout()
  router.push('/login')
}
</script>

<template>
  <div class="layout">
    <aside :class="['sidebar', { collapsed: sidebarCollapsed }]">
      <div class="sidebar-header">
        <span v-if="!sidebarCollapsed" class="logo">Parade</span>
        <button class="toggle-btn" @click="sidebarCollapsed = !sidebarCollapsed">
          {{ sidebarCollapsed ? '→' : '←' }}
        </button>
      </div>

      <nav class="nav">
        <router-link
          v-for="item in navItems"
          :key="item.path"
          :to="item.path"
          :class="['nav-item', { active: isActive(item.path) }]"
        >
          <span class="nav-icon">{{ item.icon }}</span>
          <span v-if="!sidebarCollapsed" class="nav-label">{{ item.name }}</span>
        </router-link>
      </nav>

      <div class="sidebar-footer">
        <div v-if="!sidebarCollapsed" class="team-info">
          <div class="team-name">{{ authStore.currentTeam?.name ?? 'No Team' }}</div>
          <div class="peer-count">
            <span>{{ peersStore.onlinePeerCount }} online</span>
          </div>
        </div>
        <button class="logout-btn" @click="handleLogout">
          {{ sidebarCollapsed ? '🚪' : 'Logout' }}
        </button>
      </div>
    </aside>

    <div class="main-area">
      <header class="header">
        <div class="header-title">
          <h1>{{ route.name }}</h1>
        </div>
        <div class="header-actions">
          <div class="connection-status">
            <span class="status-dot online" />
            <span>Connected</span>
          </div>
        </div>
      </header>

      <main class="content">
        <slot />
      </main>
    </div>

    <aside class="right-panel">
      <div class="panel-header">
        <h3>Peers</h3>
      </div>
      <div class="peer-list">
        <div
          v-for="peer in peersStore.peers"
          :key="peer.pubkey"
          :class="['peer-item', peer.status]"
        >
          <span class="peer-dot" />
          <span class="peer-name">{{ peer.pubkey.slice(0, 8) }}...</span>
        </div>
        <div v-if="peersStore.peers.length === 0" class="no-peers">
          No peers connected
        </div>
      </div>
    </aside>
  </div>
</template>

<style scoped>
.layout {
  display: grid;
  grid-template-columns: auto 1fr auto;
  height: 100vh;
  overflow: hidden;
}

.sidebar {
  width: 220px;
  display: flex;
  flex-direction: column;
  background: #111827;
  border-right: 1px solid #374151;
  transition: width 0.2s;
}

.sidebar.collapsed {
  width: 60px;
}

.sidebar-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem;
  border-bottom: 1px solid #374151;
}

.logo {
  font-size: 1.25rem;
  font-weight: 700;
  color: #6366f1;
}

.toggle-btn {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #374151;
  border: none;
  border-radius: 6px;
  color: #9ca3af;
  cursor: pointer;
}

.toggle-btn:hover {
  background: #4b5563;
  color: #f9fafb;
}

.nav {
  flex: 1;
  padding: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.75rem;
  border-radius: 6px;
  color: #9ca3af;
  text-decoration: none;
  transition: background 0.2s;
}

.nav-item:hover {
  background: #1f2937;
  color: #f9fafb;
}

.nav-item.active {
  background: #3730a3;
  color: #f9fafb;
}

.nav-icon {
  font-size: 1.25rem;
}

.nav-label {
  font-size: 0.875rem;
  font-weight: 500;
}

.sidebar-footer {
  padding: 1rem;
  border-top: 1px solid #374151;
}

.team-info {
  margin-bottom: 0.75rem;
}

.team-name {
  font-size: 0.875rem;
  font-weight: 500;
  color: #f9fafb;
}

.peer-count {
  font-size: 0.75rem;
  color: #6b7280;
}

.logout-btn {
  width: 100%;
  padding: 0.5rem;
  background: #374151;
  border: none;
  border-radius: 6px;
  color: #f9fafb;
  font-size: 0.875rem;
  cursor: pointer;
}

.logout-btn:hover {
  background: #4b5563;
}

.main-area {
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 1rem 1.5rem;
  background: #1f2937;
  border-bottom: 1px solid #374151;
}

.header-title h1 {
  margin: 0;
  font-size: 1.25rem;
  font-weight: 600;
  color: #f9fafb;
  text-transform: capitalize;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 1rem;
}

.connection-status {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.875rem;
  color: #9ca3af;
}

.status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #6b7280;
}

.status-dot.online {
  background: #10b981;
}

.status-dot.connecting {
  background: #f59e0b;
}

.content {
  flex: 1;
  overflow: auto;
  background: #1a1a2e;
}

.right-panel {
  width: 240px;
  display: flex;
  flex-direction: column;
  background: #111827;
  border-left: 1px solid #374151;
}

.panel-header {
  padding: 1rem;
  border-bottom: 1px solid #374151;
}

.panel-header h3 {
  margin: 0;
  font-size: 0.875rem;
  font-weight: 600;
  color: #f9fafb;
}

.peer-list {
  flex: 1;
  overflow-y: auto;
  padding: 0.5rem;
}

.peer-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem;
  border-radius: 6px;
  font-size: 0.875rem;
  color: #9ca3af;
}

.peer-item.online {
  color: #f9fafb;
}

.peer-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #6b7280;
}

.peer-item.online .peer-dot {
  background: #10b981;
}

.peer-item.offline .peer-dot {
  background: #6b7280;
}

.no-peers {
  padding: 1rem;
  text-align: center;
  font-size: 0.875rem;
  color: #6b7280;
}
</style>
