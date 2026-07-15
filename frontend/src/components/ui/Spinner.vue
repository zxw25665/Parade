<script setup lang="ts">
interface Props {
  size?: 'sm' | 'md' | 'lg'
}

withDefaults(defineProps<Props>(), {
  size: 'md',
})
</script>

<template>
  <div :class="['spinner-wrapper', `spinner-${size}`]">
    <div class="spinner" />
  </div>
</template>

<style scoped>
.spinner-wrapper {
  display: inline-flex;
  align-items: center;
  justify-content: center;
}

.spinner {
  border: 2px solid #374151;
  border-top-color: #6366f1;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

.spinner-sm .spinner {
  width: 1rem;
  height: 1rem;
}

.spinner-md .spinner {
  width: 2rem;
  height: 2rem;
}

.spinner-lg .spinner {
  width: 3rem;
  height: 3rem;
  border-width: 3px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
