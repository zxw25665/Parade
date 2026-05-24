<template>
  <div class="panel-section">
    <div class="panel-section-header" @click="open = !open">
      <span>{{ title }}</span>
      <span class="chevron" :class="{ open }">▶</span>
    </div>
    <div class="panel-section-body" :class="{ open, collapsed: !open }">
      <slot />
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

const props = defineProps({
  title: { type: String, required: true },
  defaultOpen: { type: Boolean, default: false }
})

const open = ref(props.defaultOpen)
</script>
