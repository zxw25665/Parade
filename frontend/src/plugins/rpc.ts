import type { App, InjectionKey } from 'vue'
import { ParadeRPC, createTypedEventHandlers } from '@/lib/rpc-client'
import { useChatStore } from '@/stores/chat'
import { usePeersStore } from '@/stores/peers'
import { useFilesStore } from '@/stores/files'
import type {
  PeerEventPayload,
  NewMessageEventPayload,
  FileProgressEventPayload,
  FileCompletedEventPayload,
  PeerStatusEventPayload,
} from '@/lib/types'

export const RPC_KEY: InjectionKey<ParadeRPC> = Symbol('parade-rpc')

let rpcInstance: ParadeRPC | null = null

export function createRPCPlugin() {
  return {
    install(app: App) {
      const rpc = new ParadeRPC({ debug: true })
      rpcInstance = rpc

      app.provide(RPC_KEY, rpc)

      const typedHandlers = createTypedEventHandlers(rpc)

      typedHandlers.onPeerJoined((payload: PeerEventPayload) => {
        const peersStore = usePeersStore()
        peersStore.handlePeerJoined(payload)
      })

      typedHandlers.onPeerLeft((payload: PeerEventPayload) => {
        const peersStore = usePeersStore()
        peersStore.handlePeerLeft(payload)
      })

      typedHandlers.onNewMessage((payload: NewMessageEventPayload) => {
        const chatStore = useChatStore()
        chatStore.handleNewMessage({
          id: payload.id,
          hlc: payload.hlc,
          sender: payload.sender,
          content: payload.content,
          conversation_id: payload.conversation_id,
          timestamp: payload.timestamp,
        })
      })

      typedHandlers.onFileProgress((payload: FileProgressEventPayload) => {
        const filesStore = useFilesStore()
        filesStore.handleFileProgress(payload)
      })

      typedHandlers.onFileCompleted((payload: FileCompletedEventPayload) => {
        const filesStore = useFilesStore()
        if (typeof payload === 'string') {
          filesStore.handleFileCompleted(payload)
        }
      })

      typedHandlers.onPeerStatus((payload: PeerStatusEventPayload) => {
        const peersStore = usePeersStore()
        peersStore.handlePeerStatus(payload)
      })

      typedHandlers.onConversationUpdated(() => {
        const chatStore = useChatStore()
        chatStore.handleConversationUpdated()
      })

      rpc.onStateChange((state) => {
        console.log('[RPC] Connection state:', state)
      })

      app.config.globalProperties.$rpc = rpc
    },
  }
}

export function useRPC(): ParadeRPC {
  const rpc = rpcInstance
  if (!rpc) {
    throw new Error('RPC not initialized. Make sure createRPCPlugin is installed.')
  }
  return rpc
}

const DAEMON_STARTUP_TIMEOUT_MS = 70000 // 60s max daemon spawn + 10s grace

/**
 * Waits for the Tauri IPC bridge to be fully initialized before any
 * listen() / invoke() calls are made. Without this barrier, listen()
 * can hang permanently because its internal IPC message to the Rust
 * backend has no handler registered yet.
 *
 * Three-layer defense:
 * 1. Fast path: __TAURI_INTERNALS__ already injected → resolve immediately
 * 2. Event: tauriReady custom event (Tauri v2.2.3+)
 * 3. Poll: check every 10ms for 3s as fallback
 */
async function waitForTauriReady(): Promise<void> {
  return new Promise<void>((resolve) => {
    const w = window as unknown as Record<string, unknown>

    // Layer 1: already injected
    if (w.__TAURI_INTERNALS__ || w.__TAURI__) {
      resolve()
      return
    }

    let settled = false
    const done = () => {
      if (settled) return
      settled = true
      clearTimeout(hardTimeout)
      clearInterval(pollInterval)
      window.removeEventListener('tauriReady', onReady)
      resolve()
    }

    // Layer 2: tauriReady event
    const onReady = () => { done() }
    window.addEventListener('tauriReady', onReady)

    // Layer 3: poll (catches race where event fired before addEventListener)
    let retries = 0
    const pollInterval = setInterval(() => {
      retries++
      if (w.__TAURI_INTERNALS__ || w.__TAURI__) {
        done()
        return
      }
      if (retries > 300) {
        console.warn('[RPC] Tauri IPC not detected after 3s — continuing anyway')
        done()
      }
    }, 10)

    // Hard timeout (10s)
    const hardTimeout = setTimeout(() => {
      console.warn('[RPC] Tauri IPC timeout after 10s')
      done()
    }, 10_000)
  })
}

async function pollDaemonReady(rpc: ParadeRPC): Promise<void> {
  const deadline = Date.now() + DAEMON_STARTUP_TIMEOUT_MS
  console.log('[RPC] pollDaemonReady: starting health-check poll...')
  let attempt = 0
  while (Date.now() < deadline) {
    attempt++
    try {
      const hasId = await rpc.checkHasIdentity()
      console.log(`[RPC] pollDaemonReady: daemon responded on attempt ${attempt}, hasIdentity=${hasId}`)
      return
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (attempt <= 3 || attempt % 10 === 0) {
        console.log(`[RPC] pollDaemonReady: attempt ${attempt} failed: ${msg}`)
      }
      await new Promise(r => setTimeout(r, 500))
    }
  }
  throw new Error(`Daemon health check timed out after ${attempt} attempts`)
}

export async function initRPCConnection(): Promise<void> {
  const rpc = useRPC()
  console.log('[RPC] initRPCConnection: waiting for Tauri IPC...')

  // Barrier 1: ensure Tauri IPC bridge is ready before calling listen()/invoke()
  await waitForTauriReady()
  console.log('[RPC] initRPCConnection: Tauri IPC ready, calling rpc.connect()...')

  // Barrier 2: register event listeners — now safe because IPC is ready
  await rpc.connect()
  console.log('[RPC] initRPCConnection: rpc.connect() done, state =', rpc.getState())

  // Barrier 3: poll daemon with an actual RPC call until it responds
  try {
    await pollDaemonReady(rpc)
    console.log('[RPC] initRPCConnection: daemon health check passed')
  } catch (err) {
    const message = err instanceof Error ? err.message : 'Unknown error'
    console.error('[RPC] initRPCConnection: daemon health check failed:', message)
  }
}
