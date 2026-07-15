<script setup lang="ts">
interface Props {
  modelValue: string
  label?: string
  placeholder?: string
  type?: 'text' | 'password' | 'email' | 'number'
  error?: string
  disabled?: boolean
  required?: boolean
}

withDefaults(defineProps<Props>(), {
  type: 'text',
  disabled: false,
  required: false,
})

defineEmits<{
  'update:modelValue': [value: string]
}>()
</script>

<template>
  <div :class="['input-wrapper', { 'has-error': error }]">
    <label v-if="label" class="input-label">
      {{ label }}
      <span v-if="required" class="required">*</span>
    </label>
    <input
      :type="type"
      :value="modelValue"
      :placeholder="placeholder"
      :disabled="disabled"
      :required="required"
      class="input-field"
      @input="$emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <span v-if="error" class="input-error">{{ error }}</span>
  </div>
</template>

<style scoped>
.input-wrapper {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
}

.input-label {
  font-size: 0.875rem;
  font-weight: 500;
  color: #d1d5db;
}

.required {
  color: #ef4444;
}

.input-field {
  padding: 0.5rem 0.75rem;
  background: #1f2937;
  border: 1px solid #374151;
  border-radius: 6px;
  color: #f9fafb;
  font-size: 1rem;
  transition: border-color 0.2s;
}

.input-field:focus {
  outline: none;
  border-color: #6366f1;
}

.input-field:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.has-error .input-field {
  border-color: #ef4444;
}

.input-error {
  font-size: 0.75rem;
  color: #ef4444;
}
</style>
