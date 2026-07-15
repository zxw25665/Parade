<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Message } from '@/lib/types'

const props = defineProps<{
  message: Message
  isOwn: boolean
  showSender: boolean
}>()

const copied = ref(false)

const formattedTime = computed(() => {
  const date = new Date(props.message.timestamp)
  return date.toLocaleTimeString([], { 
    hour: '2-digit', 
    minute: '2-digit' 
  })
})

const senderName = computed(() => {
  if (props.isOwn) return 'You'
  
  const sender = props.message.sender
  if (!sender) return 'Unknown'
  
  // Truncate pubkey
  if (sender.length > 12) {
    return sender.slice(0, 8) + '…'
  }
  return sender
})

async function copyContent() {
  try {
    await navigator.clipboard.writeText(props.message.content)
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 2000)
  } catch (err) {
    console.error('Failed to copy:', err)
  }
}
</script>

<template>
  <div 
    class="message-bubble"
    :class="{ own: isOwn, incoming: !isOwn }"
  >
    <div class="message-content">
      <!-- Sender name for incoming messages -->
      <div v-if="showSender && !isOwn" class="sender-name">
        {{ senderName }}
      </div>
      
      <!-- Message body -->
      <div class="bubble">
        <p class="message-text">{{ message.content }}</p>
        
        <!-- Actions (visible on hover) -->
        <div class="message-actions">
          <button 
            class="action-btn"
            @click="copyContent"
            :title="copied ? 'Copied!' : 'Copy'"
          >
            <svg v-if="!copied" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
              <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1"/>
            </svg>
            <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <polyline points="20 6 9 17 4 12"/>
            </svg>
          </button>
        </div>
      </div>
      
      <!-- Timestamp -->
      <div class="message-meta">
        <span class="timestamp">{{ formattedTime }}</span>
        <span v-if="isOwn" class="own-indicator">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="20 6 9 17 4 12"/>
          </svg>
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.message-bubble {
  display: flex;
  max-width: 75%;
  animation: messageIn 0.25s ease-out;
}

.message-bubble.own {
  margin-left: auto;
}

.message-bubble.incoming {
  margin-right: auto;
}

@keyframes messageIn {
  from {
    opacity: 0;
    transform: translateY(8px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}

/* Sender Name */
.sender-name {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--primary);
  margin-bottom: var(--space-1);
  padding-left: var(--space-2);
}

/* Message Content Container */
.message-content {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

/* Bubble */
.bubble {
  position: relative;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-lg);
  word-wrap: break-word;
  overflow-wrap: break-word;
}

/* Incoming bubble */
.incoming .bubble {
  background: var(--bg-elevated);
  border-bottom-left-radius: var(--radius-sm);
}

/* Own bubble */
.own .bubble {
  background: linear-gradient(135deg, var(--primary-muted), rgba(79, 70, 229, 0.2));
  border-bottom-right-radius: var(--radius-sm);
}

.message-text {
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--text-primary);
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}

/* Message Actions */
.message-actions {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  right: -40px;
  display: flex;
  gap: var(--space-1);
  opacity: 0;
  transition: opacity var(--transition-fast);
}

.message-bubble:hover .message-actions {
  opacity: 1;
}

.action-btn {
  width: 28px;
  height: 28px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.action-btn:hover {
  background: var(--bg-overlay);
  color: var(--text-primary);
  border-color: var(--border-hover);
}

/* Message Meta */
.message-meta {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: 0 var(--space-2);
}

.incoming .message-meta {
  justify-content: flex-start;
}

.own .message-meta {
  justify-content: flex-end;
}

.timestamp {
  font-size: 10px;
  color: var(--text-disabled);
}

.own-indicator {
  display: flex;
  align-items: center;
  color: var(--success);
}

/* Responsive */
@media (max-width: 640px) {
  .message-bubble {
    max-width: 85%;
  }
  
  .message-actions {
    right: -32px;
  }
  
  .action-btn {
    width: 24px;
    height: 24px;
  }
}
</style>
