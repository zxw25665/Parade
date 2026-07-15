<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'

const props = withDefaults(defineProps<{
  placeholder?: string
  disabled?: boolean
  maxLength?: number
}>(), {
  placeholder: 'Type a message...',
  disabled: false,
  maxLength: 4000,
})

const emit = defineEmits<{
  send: [text: string]
}>()

const inputRef = ref<HTMLTextAreaElement | null>(null)
const text = ref('')
const isFocused = ref(false)

const charCount = computed(() => text.value.length)
const canSend = computed(() => text.value.trim().length > 0 && !props.disabled)
const isOverLimit = computed(() => charCount.value > props.maxLength)

watch(text, async () => {
  // Auto-resize textarea
  if (inputRef.value) {
    inputRef.value.style.height = 'auto'
    const newHeight = Math.min(inputRef.value.scrollHeight, 150) // Max 150px
    inputRef.value.style.height = newHeight + 'px'
  }
})

function handleKeydown(e: KeyboardEvent) {
  // Send on Enter (without Shift)
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

function send() {
  const content = text.value.trim()
  if (!content || props.disabled || isOverLimit.value) return
  
  emit('send', content)
  text.value = ''
  
  // Reset height
  if (inputRef.value) {
    inputRef.value.style.height = 'auto'
  }
  
  // Refocus
  nextTick(() => {
    inputRef.value?.focus()
  })
}

function handlePaste(e: ClipboardEvent) {
  // Allow paste, but let the input handle it
  // We just ensure the textarea can receive paste events
}

function focus() {
  inputRef.value?.focus()
}

defineExpose({ focus })
</script>

<template>
  <div class="chat-input" :class="{ focused: isFocused, disabled }">
    <div class="input-container">
      <textarea
        ref="inputRef"
        v-model="text"
        :placeholder="placeholder"
        :disabled="disabled"
        :maxlength="maxLength"
        rows="1"
        class="input-field"
        @keydown="handleKeydown"
        @focus="isFocused = true"
        @blur="isFocused = false"
      ></textarea>
      
      <!-- Character count (shown when near limit) -->
      <div 
        v-if="charCount > maxLength * 0.8" 
        class="char-count"
        :class="{ warning: charCount > maxLength * 0.9, error: isOverLimit }"
      >
        {{ charCount }}/{{ maxLength }}
      </div>
    </div>
    
    <div class="input-actions">
      <!-- Emoji button (future) -->
      <button 
        class="action-btn" 
        disabled
        title="Emoji (coming soon)"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <path d="M8 14s1.5 2 4 2 4-2 4-2"/>
          <line x1="9" y1="9" x2="9.01" y2="9"/>
          <line x1="15" y1="9" x2="15.01" y2="9"/>
        </svg>
      </button>
      
      <!-- Send button -->
      <button 
        class="send-btn"
        :disabled="!canSend || isOverLimit"
        @click="send"
        title="Send message (Enter)"
      >
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <line x1="22" y1="2" x2="11" y2="13"/>
          <polygon points="22 2 15 22 11 13 2 9 22 2"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped>
.chat-input {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--bg-surface);
  border-top: 1px solid var(--border-default);
}

.input-container {
  flex: 1;
  position: relative;
  display: flex;
  flex-direction: column;
}

.input-field {
  width: 100%;
  min-height: 40px;
  max-height: 150px;
  padding: var(--space-2) var(--space-3);
  background: var(--bg-elevated);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-lg);
  color: var(--text-primary);
  font-size: var(--text-sm);
  line-height: 1.5;
  resize: none;
  transition: all var(--transition-fast);
}

.input-field:focus {
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.input-field::placeholder {
  color: var(--text-muted);
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.focused .input-field {
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.char-count {
  position: absolute;
  bottom: 4px;
  right: 8px;
  font-size: 10px;
  color: var(--text-muted);
  pointer-events: none;
}

.char-count.warning {
  color: var(--warning);
}

.char-count.error {
  color: var(--error);
}

/* Actions */
.input-actions {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.action-btn {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: transparent;
  border-radius: var(--radius-md);
  color: var(--text-muted);
  transition: all var(--transition-fast);
}

.action-btn:hover:not(:disabled) {
  background: var(--bg-elevated);
  color: var(--text-secondary);
}

.action-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.send-btn {
  width: 40px;
  height: 40px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--primary);
  border-radius: var(--radius-md);
  color: white;
  transition: all var(--transition-fast);
}

.send-btn:hover:not(:disabled) {
  background: var(--primary-hover);
  transform: scale(1.05);
}

.send-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
  transform: none;
}

/* Disabled state */
.chat-input.disabled {
  opacity: 0.5;
  pointer-events: none;
}

/* Responsive */
@media (max-width: 640px) {
  .chat-input {
    padding: var(--space-2) var(--space-3);
  }
  
  .action-btn,
  .send-btn {
    width: 36px;
    height: 36px;
  }
}
</style>
