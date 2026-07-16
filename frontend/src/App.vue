<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'
import MainLayout from '@/layouts/MainLayout.vue'

const route = useRoute()

// =========================================================================
// Debug overlay — shows RPC state, errors, and health check button
// =========================================================================
const showDebug = ref(false)
const debugRpcState = ref('...')
const debugLog = ref<string[]>([])
const debugPolling = ref(false)

function addDebug(msg: string) {
  const time = new Date().toLocaleTimeString()
  debugLog.value.push(`[${time}] ${msg}`)
  if (debugLog.value.length > 50) debugLog.value.shift()
  console.log('[DebugPanel]', msg)
}

let pollInterval: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  // Auto-show debug panel for first 60s during startup
  showDebug.value = true
  startPolling()
  setTimeout(() => {
    // Auto-hide after 60s if everything is connected
    if (debugRpcState.value === 'connected') {
      showDebug.value = false
      stopPolling()
    }
  }, 60_000)

  // Keyboard shortcut: Ctrl+Shift+D to toggle
  function onKey(e: KeyboardEvent) {
    if (e.ctrlKey && e.shiftKey && e.key === 'D') {
      showDebug.value = !showDebug.value
      if (showDebug.value) startPolling()
      else stopPolling()
    }
  }
  window.addEventListener('keydown', onKey)

  onUnmounted(() => {
    window.removeEventListener('keydown', onKey)
    stopPolling()
  })
})

function startPolling() {
  if (pollInterval) return
  addDebug('Debug panel opened')
  pollInterval = setInterval(updateState, 1000)
  updateState()
}

function stopPolling() {
  if (pollInterval) {
    clearInterval(pollInterval)
    pollInterval = null
  }
}

function updateState() {
  try {
    const w = window as unknown as Record<string, unknown>
    const parade = w.__parade as Record<string, unknown> | undefined
    if (parade && typeof parade.state === 'function') {
      const s = (parade.state as () => Record<string, unknown>)()
      debugRpcState.value = String(s.rpcState ?? 'unknown')
    } else {
      debugRpcState.value = 'loading...'
    }
  } catch {
    debugRpcState.value = 'error'
  }
}

async function runHealthCheck() {
  debugPolling.value = true
  addDebug('=== Health Check Started ===')
  try {
    const w = window as unknown as Record<string, unknown>
    const parade = w.__parade as Record<string, unknown> | undefined
    if (parade && typeof (parade as Record<string, unknown>).healthCheck === 'function') {
      await ((parade as Record<string, unknown>).healthCheck as () => Promise<void>)()
      addDebug('Health check completed — check browser console for details')
    } else {
      addDebug('ERROR: __parade not available yet')
    }
  } catch (e) {
    addDebug(`Health check error: ${e}`)
  } finally {
    debugPolling.value = false
  }
}

async function runManualCheck() {
  debugPolling.value = true
  addDebug('=== Manual check_has_identity ===')
  try {
    const w = window as unknown as Record<string, unknown>
    const parade = w.__parade as Record<string, unknown> | undefined
    const rpc = parade?.rpc as Record<string, unknown> | undefined
    if (rpc && typeof rpc.checkHasIdentity === 'function') {
      const start = performance.now()
      const result = await (rpc.checkHasIdentity as () => Promise<boolean>)()
      addDebug(`checkHasIdentity → ${result} (${(performance.now() - start).toFixed(0)}ms)`)
    } else {
      addDebug('ERROR: RPC not available')
    }
  } catch (e) {
    addDebug(`FAILED: ${e instanceof Error ? e.message : String(e)}`)
  } finally {
    debugPolling.value = false
  }
}
</script>

<template>
  <MainLayout v-if="route.meta.layout === 'main'">
    <RouterView />
  </MainLayout>
  <RouterView v-else />

  <!-- Debug overlay (Ctrl+Shift+D or ?debug in URL) -->
  <div v-if="showDebug" class="debug-panel">
    <div class="debug-header">
      <span class="debug-title">🔧 Debug</span>
      <span class="debug-state" :class="debugRpcState">
        RPC: {{ debugRpcState }}
      </span>
      <button class="debug-close" @click="showDebug = false">✕</button>
    </div>
    <div class="debug-actions">
      <button :disabled="debugPolling" @click="runHealthCheck">Health Check</button>
      <button :disabled="debugPolling" @click="runManualCheck">check_has_identity</button>
    </div>
    <div class="debug-log">
      <div v-for="(line, i) in debugLog" :key="i" class="debug-line">{{ line }}</div>
    </div>
  </div>
</template>

<style>
/* Debug panel — fixed overlay, top-right corner */
.debug-panel {
  position: fixed;
  top: 8px;
  right: 8px;
  width: 380px;
  max-height: 60vh;
  background: rgba(15, 23, 42, 0.95);
  border: 1px solid rgba(96, 165, 250, 0.4);
  border-radius: 8px;
  color: #e2e8f0;
  font-size: 12px;
  font-family: 'Consolas', 'Monaco', monospace;
  z-index: 99999;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.5);
}
.debug-header {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 10px;
  border-bottom: 1px solid rgba(96, 165, 250, 0.2);
}
.debug-title { font-weight: bold; color: #60a5fa; }
.debug-state { padding: 2px 8px; border-radius: 4px; font-size: 11px; }
.debug-state.connected { background: rgba(16, 185, 129, 0.2); color: #34d399; }
.debug-state.connecting { background: rgba(251, 191, 36, 0.2); color: #fbbf24; }
.debug-state.disconnected { background: rgba(239, 68, 68, 0.2); color: #f87171; }
.debug-state.error { background: rgba(239, 68, 68, 0.3); color: #f87171; }
.debug-close { margin-left: auto; background: none; border: none; color: #94a3b8; cursor: pointer; font-size: 14px; }
.debug-actions { display: flex; gap: 4px; padding: 6px 10px; border-bottom: 1px solid rgba(96, 165, 250, 0.2); }
.debug-actions button {
  padding: 3px 8px;
  background: rgba(96, 165, 250, 0.15);
  border: 1px solid rgba(96, 165, 250, 0.3);
  border-radius: 4px;
  color: #93c5fd;
  font-size: 11px;
  cursor: pointer;
}
.debug-actions button:hover:not(:disabled) { background: rgba(96, 165, 250, 0.3); }
.debug-actions button:disabled { opacity: 0.4; cursor: default; }
.debug-log { flex: 1; overflow-y: auto; padding: 6px 10px; max-height: 300px; }
.debug-line { padding: 1px 0; color: #94a3b8; white-space: pre-wrap; word-break: break-all; }
.debug-line:has(ERROR) { color: #f87171; }
.debug-line:has(FAILED) { color: #f87171; }
</style>
