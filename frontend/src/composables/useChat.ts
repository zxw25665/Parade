import { computed, ref } from 'vue'
import { useChatStore } from '@/stores/chat'
import type { Message, Conversation } from '@/lib/types'

export function useChat() {
  const chatStore = useChatStore()

  const conversations = computed(() => chatStore.conversations)
  const selectedConvId = computed(() => chatStore.selectedConvId)
  const selectedConversation = computed(() => chatStore.selectedConversation)
  const selectedMessages = computed(() => chatStore.selectedMessages)
  const teamConversations = computed(() => chatStore.teamConversations)
  const privateConversations = computed(() => chatStore.privateConversations)
  const sortedConversations = computed(() => chatStore.sortedConversations)
  const loading = computed(() => chatStore.loading)
  const loadingMessages = computed(() => chatStore.loadingMessages)
  const error = computed(() => chatStore.error)

  async function loadConversations() {
    await chatStore.loadConversations()
  }

  async function loadMessages(convId: string, limit?: number) {
    await chatStore.loadMessages(convId, limit)
  }

  async function loadMoreMessages(convId: string) {
    await chatStore.loadMoreMessages(convId)
  }

  async function sendTeamMessage(text: string) {
    await chatStore.sendTeamMessage(text)
  }

  async function sendPrivateMessage(targetUUID: string, text: string) {
    await chatStore.sendPrivateMessage(targetUUID, text)
  }

  async function startPrivateConversation(peerUUID: string): Promise<string> {
    return await chatStore.startPrivateConversation(peerUUID)
  }

  function selectConversation(convId: string | null) {
    chatStore.selectConversation(convId)
  }

  function handleNewMessage(message: Message) {
    chatStore.handleNewMessage(message)
  }

  function handleConversationUpdated() {
    chatStore.handleConversationUpdated()
  }

  return {
    conversations,
    selectedConvId,
    selectedConversation,
    selectedMessages,
    teamConversations,
    privateConversations,
    sortedConversations,
    loading,
    loadingMessages,
    error,
    loadConversations,
    loadMessages,
    loadMoreMessages,
    sendTeamMessage,
    sendPrivateMessage,
    startPrivateConversation,
    selectConversation,
    handleNewMessage,
    handleConversationUpdated,
    clearError: chatStore.clearError,
  }
}
