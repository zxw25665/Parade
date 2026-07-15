import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ParadeRPC } from '@/lib/rpc-client'
import type { Conversation, Message } from '@/lib/types'

export const useChatStore = defineStore('chat', () => {
  const conversations = ref<Conversation[]>([])
  const messages = ref<Record<string, Message[]>>({})
  const selectedConvId = ref<string | null>(null)
  const loading = ref(false)
  const loadingMessages = ref(false)
  const error = ref<string | null>(null)
  const messagePagination = ref<Record<string, { hasMore: boolean; offset: number }>>({})

  let rpc: ParadeRPC | null = null

  const selectedConversation = computed(() => {
    if (!selectedConvId.value) return null
    return conversations.value.find(c => c.id === selectedConvId.value) ?? null
  })

  const selectedMessages = computed(() => {
    if (!selectedConvId.value) return []
    return messages.value[selectedConvId.value] ?? []
  })

  const teamConversations = computed(() => conversations.value.filter(c => c.type === 'team'))
  const privateConversations = computed(() => conversations.value.filter(c => c.type === 'private'))

  const sortedConversations = computed(() => {
    return [...conversations.value].sort((a, b) => {
      const timeA = a.last_msg_time ?? a.created_at
      const timeB = b.last_msg_time ?? b.created_at
      return timeB.localeCompare(timeA)
    })
  })

  function setRPC(rpcInstance: ParadeRPC) {
    rpc = rpcInstance
  }

  async function loadConversations(): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      conversations.value = await rpc.listConversations()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load conversations'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function loadMessages(convId: string, limit = 50, prepend = false): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loadingMessages.value = true
    error.value = null
    try {
      const offset = prepend ? 0 : (messagePagination.value[convId]?.offset ?? 0)
      const newMessages = await rpc.getConversationMessages(convId, limit, offset)
      if (prepend) {
        const existing = messages.value[convId] ?? []
        messages.value[convId] = [...newMessages, ...existing]
        messagePagination.value[convId] = {
          hasMore: newMessages.length === limit,
          offset: newMessages.length,
        }
      } else {
        const existing = messages.value[convId] ?? []
        messages.value[convId] = [...existing, ...newMessages]
        messagePagination.value[convId] = {
          hasMore: newMessages.length === limit,
          offset: offset + newMessages.length,
        }
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load messages'
      throw e
    } finally {
      loadingMessages.value = false
    }
  }

  async function loadMoreMessages(convId: string): Promise<void> {
    if (!messagePagination.value[convId]?.hasMore) return
    await loadMessages(convId, 50, true)
  }

  async function sendTeamMessage(text: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.sendTeamChat(text)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to send message'
      throw e
    }
  }

  async function sendPrivateMessage(targetUUID: string, text: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.sendPrivateChat(targetUUID, text)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to send message'
      throw e
    }
  }

  async function startPrivateConversation(peerUUID: string): Promise<string> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      const convId = await rpc.startPrivateConversation(peerUUID)
      await loadConversations()
      return convId
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to start conversation'
      throw e
    }
  }

  function selectConversation(convId: string | null): void {
    selectedConvId.value = convId
    if (convId && !messages.value[convId]) {
      messages.value[convId] = []
      messagePagination.value[convId] = { hasMore: true, offset: 0 }
      loadMessages(convId, 50)
    }
  }

  function handleNewMessage(message: Message): void {
    const convId = message.conversation_id
    const existing = messages.value[convId] ?? []
    messages.value[convId] = [...existing, message]
    const conv = conversations.value.find(c => c.id === convId)
    if (conv) {
      conv.last_message = message.content
      conv.last_msg_time = message.timestamp
      conv.last_hlc = message.hlc
    }
  }

  function handleConversationUpdated(): void {
    loadConversations()
  }

  function clearConversationMessages(convId: string): void {
    delete messages.value[convId]
    delete messagePagination.value[convId]
  }

  function clearAllMessages(): void {
    messages.value = {}
    messagePagination.value = {}
  }

  function clearError(): void {
    error.value = null
  }

  return {
    conversations,
    messages,
    selectedConvId,
    loading,
    loadingMessages,
    error,
    selectedConversation,
    selectedMessages,
    teamConversations,
    privateConversations,
    sortedConversations,
    setRPC,
    loadConversations,
    loadMessages,
    loadMoreMessages,
    sendTeamMessage,
    sendPrivateMessage,
    startPrivateConversation,
    selectConversation,
    handleNewMessage,
    handleConversationUpdated,
    clearConversationMessages,
    clearAllMessages,
    clearError,
  }
})
