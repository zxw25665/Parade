import type { App, InjectionKey } from 'vue'
import { ParadeRPC, createTypedEventHandlers } from '@/lib/rpc-client'
import { useAuthStore } from '@/stores/auth'
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
      const rpc = new ParadeRPC({ debug: false })
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

export async function initRPCConnection(): Promise<void> {
  const rpc = useRPC()
  await rpc.connect()

  const authStore = useAuthStore()
  authStore.setRPC(rpc)

  const chatStore = useChatStore()
  chatStore.setRPC(rpc)

  const peersStore = usePeersStore()
  peersStore.setRPC(rpc)

  const filesStore = useFilesStore()
  filesStore.setRPC(rpc)
}
