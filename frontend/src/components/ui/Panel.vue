<script setup lang="ts">
import { ref } from 'vue'

interface Props {
  title: string
  defaultOpen?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  defaultOpen: true,
})

const isOpen = ref(props.defaultOpen)

function toggle() {
  isOpen.value = !isOpen.value
}
</script>

<template>
  <div :class="['panel', { 'panel-open': isOpen }]">
    <button class="panel-header" @click="toggle">
      <span class="panel-title">{{ title }}</span>
      <span class="panel-arrow">{{ isOpen ? '▼' : '▶' }}</span>
    </button>
    <div v-show="isOpen" class="panel-content">
      <slot />
    </div>
  </div>
</template>

<style scoped>
.panel {
  border: 1px solid #374151;
  border-radius: 8px;
  overflow: hidden;
}

.panel-header {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.75rem 1rem;
  background: #1f2937;
  border: none;
  color: #f9fafb;
  font-size: 1rem;
  font-weight: 500;
  cursor: pointer;
  text-align: left;
}

.panel-header:hover {
  background: #283548;
}

.panel-title {
  flex: 1;
}

.panel-arrow {
  font-size: 0.75rem;
  color: #9ca3af;
  transition: transform 0.2s;
}

.panel-content {
  padding: 1rem;
  border-top: 1px solid #374151;
}
</style>
