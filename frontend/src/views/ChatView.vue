<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useChatStore } from '@/stores/chat'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'

const { t } = useI18n()
const chatStore = useChatStore()
</script>

<template>
  <div class="chat-view">
    <div class="chat-header">
      <h2>{{ t('chat.title') }}</h2>
    </div>
    <div class="chat-container">
      <aside class="conversation-list">
        <div class="section-title">{{ t('chat.conversations') }}</div>
        <div v-if="chatStore.conversations.length === 0" class="empty-state">
          {{ t('chat.noConversations') }}
        </div>
        <div v-for="conv in chatStore.conversations" :key="conv.id" class="conversation-item">
          {{ conv.display_name }}
        </div>
      </aside>
      <main class="chat-main">
        <div v-if="!chatStore.selectedConvId" class="empty-state">
          {{ t('chat.selectConversation') }}
        </div>
        <div v-else class="message-list">
          <div v-for="msg in chatStore.selectedMessages" :key="msg.id" class="message">
            {{ msg.content }}
          </div>
        </div>
      </main>
    </div>
  </div>
</template>

<style scoped>
.chat-view {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.chat-header {
  padding: 1rem 1.5rem;
  border-bottom: 1px solid #374151;
}

.chat-header h2 {
  margin: 0;
  font-size: 1.25rem;
  color: #f9fafb;
}

.chat-container {
  flex: 1;
  display: grid;
  grid-template-columns: 280px 1fr;
  overflow: hidden;
}

.conversation-list {
  border-right: 1px solid #374151;
  overflow-y: auto;
  padding: 1rem;
}

.section-title {
  font-size: 0.75rem;
  font-weight: 600;
  color: #6b7280;
  text-transform: uppercase;
  margin-bottom: 0.75rem;
}

.empty-state {
  padding: 2rem;
  text-align: center;
  color: #6b7280;
  font-size: 0.875rem;
}

.conversation-item {
  padding: 0.75rem;
  margin-bottom: 0.25rem;
  border-radius: 6px;
  cursor: pointer;
  color: #d1d5db;
}

.conversation-item:hover {
  background: #1f2937;
}

.message-list {
  flex: 1;
  overflow-y: auto;
  padding: 1rem;
}

.message {
  padding: 0.5rem;
  margin-bottom: 0.5rem;
  background: #1f2937;
  border-radius: 6px;
  color: #f9fafb;
}

.chat-main {
  display: flex;
  flex-direction: column;
}
</style>
