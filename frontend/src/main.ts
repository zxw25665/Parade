import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import i18n from './i18n'
import { createRPCPlugin, useRPC, initRPCConnection } from './plugins/rpc'
import { useAuthStore } from './stores/auth'
import { useChatStore } from './stores/chat'
import { usePeersStore } from './stores/peers'
import { useFilesStore } from './stores/files'
import './styles/main.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(i18n)
app.use(createRPCPlugin())

app.mount('#app')

// Expose router for E2E test navigation (client-side routing preserves store state)
;(window as unknown as Record<string, unknown>).__parade_router = router

// =========================================================================
// CRITICAL: Inject RPC reference into ALL stores IMMEDIATELY (synchronous).
// This must happen before any component tries to call store methods.
// The actual daemon connection happens asynchronously below.
// =========================================================================
const rpc = useRPC()
const authStore = useAuthStore()
const chatStore = useChatStore()
const peersStore = usePeersStore()
const filesStore = useFilesStore()

authStore.setRPC(rpc)
chatStore.setRPC(rpc)
peersStore.setRPC(rpc)
filesStore.setRPC(rpc)

// =========================================================================
// Debug harness — exposed on window.__parade IMMEDIATELY (synchronous).
// This lets the debug panel and browser console inspect state regardless
// of whether the daemon has connected yet.
// =========================================================================
const debug = {
  rpc,
  auth: authStore,
  chat: chatStore,
  peers: peersStore,
  files: filesStore,

  state() {
    return {
      rpcState: rpc.getState(),
      isLoggedIn: authStore.isLoggedIn,
      hasIdentity: authStore.hasIdentity,
      teams: authStore.teams.length,
      conversations: chatStore.conversations.length,
      peers: peersStore.peers.length,
      onlinePeers: peersStore.onlinePeerCount,
    }
  },

  async call(method: string, ...args: unknown[]) {
    console.log(`[debug] Calling ${method} with`, args)
    try {
      const rpcAny = rpc as unknown as Record<string, (...args: unknown[]) => unknown>
      if (typeof rpcAny[method] === 'function') {
        const result = await rpcAny[method](...args)
        console.log(`[debug] ${method} →`, result)
        return result
      }
      throw new Error(`Unknown method: ${method}`)
    } catch (e) {
      console.error(`[debug] ${method} FAILED:`, e)
      throw e
    }
  },

  async healthCheck() {
    console.log('[debug] === Health Check ===')
    console.log('[debug] RPC state:', rpc.getState())
    try {
      console.log('[debug] Testing check_has_identity via rpc...')
      const start = performance.now()
      const result = await rpc.checkHasIdentity()
      console.log(`[debug] check_has_identity → ${result} (${(performance.now() - start).toFixed(0)}ms)`)
    } catch (e) {
      console.error('[debug] check_has_identity FAILED:', e)
    }
  },

  async connectNow() {
    console.log('[debug] Manually triggering rpc.connect()...')
    try {
      await rpc.connect()
      console.log('[debug] rpc.connect() succeeded, state =', rpc.getState())
    } catch (e) {
      console.error('[debug] rpc.connect() FAILED:', e)
    }
  },
}

;(window as unknown as Record<string, unknown>).__parade = debug
console.log(
  '%c[Parade] Debug harness ready. %cwindow.__parade%c available.',
  'color: #60a5fa', 'font-weight: bold; color: #fbbf24', 'color: #60a5fa'
)
console.log('  __parade.state()      — dump current state')
console.log('  __parade.healthCheck() — test RPC connectivity')
console.log('  __parade.connectNow()  — manually trigger connect')

// =========================================================================
// Start RPC connection asynchronously — do NOT block the debug harness.
// initRPCConnection calls rpc.connect() → pollDaemonReady() internally.
// =========================================================================
initRPCConnection().then(() => {
  console.log('[Parade] RPC connection initialized successfully')
}).catch((err) => {
  console.error('[Parade] RPC connection failed:', err)
})
