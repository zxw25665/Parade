<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

const password = ref('')
const confirmPassword = ref('')
const error = ref('')
const showPassword = ref(false)
const showConfirm = ref(false)

const passwordStrength = computed(() => {
  const pwd = password.value
  if (!pwd) return { score: 0, label: '', color: '' }
  
  let score = 0
  if (pwd.length >= 8) score++
  if (pwd.length >= 12) score++
  if (pwd.length >= 16) score++
  if (/[a-z]/.test(pwd) && /[A-Z]/.test(pwd)) score++
  if (/\d/.test(pwd)) score++
  if (/[^a-zA-Z0-9]/.test(pwd)) score++
  
  if (score <= 2) return { score: 1, label: 'Weak', color: 'var(--error)' }
  if (score <= 4) return { score: 2, label: 'Fair', color: 'var(--warning)' }
  if (score <= 5) return { score: 3, label: 'Good', color: 'var(--info)' }
  return { score: 4, label: 'Strong', color: 'var(--success)' }
})

const requirements = computed(() => [
  { met: password.value.length >= 8, label: 'At least 8 characters' },
  { met: /[a-z]/.test(password.value) && /[A-Z]/.test(password.value), label: 'Uppercase & lowercase letters' },
  { met: /\d/.test(password.value), label: 'At least one number' },
  { met: /[^a-zA-Z0-9]/.test(password.value), label: 'Special character' },
])

const canSubmit = computed(() => {
  return password.value.length >= 8 && 
         password.value === confirmPassword.value &&
         requirements.value.every(r => r.met)
})

async function handleRegister() {
  if (!canSubmit.value) return
  
  authStore.clearError()
  
  try {
    await authStore.register(password.value)
    await authStore.login(password.value)
    router.push('/team-join')
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to create identity'
  }
}
</script>

<template>
  <div class="setup">
    <div class="setup-backdrop"></div>
    
    <div class="setup-card">
      <button class="back-btn" @click="router.push('/')" aria-label="Go back">
        <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M9.707 16.707a1 1 0 01-1.414 0l-6-6a1 1 0 010-1.414l6-6a1 1 0 011.414 1.414L5.414 9H17a1 1 0 110 2H5.414l4.293 4.293a1 1 0 010 1.414z" clip-rule="evenodd"/>
        </svg>
      </button>

      <div class="card-header">
        <div class="icon-wrapper">
          <svg width="32" height="32" viewBox="0 0 32 32" fill="none">
            <rect x="4" y="8" width="24" height="20" rx="3" stroke="currentColor" stroke-width="2"/>
            <path d="M10 8V6a6 6 0 1112 0v2" stroke="currentColor" stroke-width="2"/>
            <circle cx="16" cy="18" r="2" fill="currentColor"/>
          </svg>
        </div>
        <h1>Create Your Identity</h1>
        <p>Your identity is encrypted with Argon2. Choose a strong password you'll remember.</p>
      </div>

      <form @submit.prevent="handleRegister" class="form">
        <div class="input-group">
          <label for="password">Password</label>
          <div class="input-wrapper">
            <input
              id="password"
              v-model="password"
              :type="showPassword ? 'text' : 'password'"
              placeholder="Enter your password"
              autocomplete="new-password"
              :disabled="authStore.loading"
            />
            <button 
              type="button" 
              class="toggle-btn"
              @click="showPassword = !showPassword"
              :aria-label="showPassword ? 'Hide password' : 'Show password'"
            >
              <svg v-if="showPassword" width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path d="M10 12a2 2 0 100-4 2 2 0 000 4z"/>
                <path fill-rule="evenodd" d="M.458 10C1.732 5.943 5.522 3 10 3s8.268 2.943 9.542 7c-1.274 4.057-5.064 7-9.542 7S1.732 14.057.458 10zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clip-rule="evenodd"/>
              </svg>
              <svg v-else width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path fill-rule="evenodd" d="M3.707 2.293a1 1 0 00-1.414 1.414l14 14a1 1 0 001.414-1.414l-1.473-1.473A10.014 10.014 0 0019.542 10C18.268 5.943 14.478 3 10 3a9.958 9.958 0 00-4.512 1.074l-1.78-1.781zm4.261 4.26l1.514 1.515a2.003 2.003 0 012.45 2.45l1.514 1.514a4 4 0 00-5.478-5.478z" clip-rule="evenodd"/>
                <path d="M12.454 16.697L9.75 13.992a4 4 0 01-3.742-3.741L2.335 6.578A9.98 9.98 0 00.458 10c1.274 4.057 5.065 7 9.542 7 .847 0 1.669-.105 2.454-.303z"/>
              </svg>
            </button>
          </div>
          
          <div class="strength-meter" v-if="password">
            <div class="strength-bars">
              <div 
                v-for="i in 4" 
                :key="i"
                class="strength-bar"
                :class="{ active: i <= passwordStrength.score }"
                :style="{ backgroundColor: i <= passwordStrength.score ? passwordStrength.color : '' }"
              ></div>
            </div>
            <span class="strength-label" :style="{ color: passwordStrength.color }">
              {{ passwordStrength.label }}
            </span>
          </div>
        </div>

        <div class="requirements">
          <div 
            v-for="req in requirements" 
            :key="req.label"
            class="requirement"
            :class="{ met: req.met }"
          >
            <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
              <path v-if="req.met" fill-rule="evenodd" d="M13.78 4.22a.75.75 0 010 1.06l-7.25 7.25a.75.75 0 01-1.06 0L2.22 9.28a.75.75 0 011.06-1.06L6 10.94l6.72-6.72a.75.75 0 011.06 0z" clip-rule="evenodd"/>
              <circle v-else cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-width="1.5" opacity="0.5"/>
            </svg>
            <span>{{ req.label }}</span>
          </div>
        </div>

        <div class="input-group">
          <label for="confirm">Confirm Password</label>
          <div class="input-wrapper">
            <input
              id="confirm"
              v-model="confirmPassword"
              :type="showConfirm ? 'text' : 'password'"
              placeholder="Confirm your password"
              autocomplete="new-password"
              :disabled="authStore.loading"
            />
            <button 
              type="button" 
              class="toggle-btn"
              @click="showConfirm = !showConfirm"
              :aria-label="showConfirm ? 'Hide password' : 'Show password'"
            >
              <svg v-if="showConfirm" width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path d="M10 12a2 2 0 100-4 2 2 0 000 4z"/>
                <path fill-rule="evenodd" d="M.458 10C1.732 5.943 5.522 3 10 3s8.268 2.943 9.542 7c-1.274 4.057-5.064 7-9.542 7S1.732 14.057.458 10zM14 10a4 4 0 11-8 0 4 4 0 018 0z" clip-rule="evenodd"/>
              </svg>
              <svg v-else width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
                <path fill-rule="evenodd" d="M3.707 2.293a1 1 0 00-1.414 1.414l14 14a1 1 0 001.414-1.414l-1.473-1.473A10.014 10.014 0 0019.542 10C18.268 5.943 14.478 3 10 3a9.958 9.958 0 00-4.512 1.074l-1.78-1.781zm4.261 4.26l1.514 1.515a2.003 2.003 0 012.45 2.45l1.514 1.514a4 4 0 00-5.478-5.478z" clip-rule="evenodd"/>
                <path d="M12.454 16.697L9.75 13.992a4 4 0 01-3.742-3.741L2.335 6.578A9.98 9.98 0 00.458 10c1.274 4.057 5.065 7 9.542 7 .847 0 1.669-.105 2.454-.303z"/>
              </svg>
            </button>
          </div>
          <div class="confirm-status" v-if="confirmPassword">
            <span v-if="password !== confirmPassword" class="mismatch">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
                <path fill-rule="evenodd" d="M10.28 5.22a.75.75 0 00-1.06 1.06L9.22 7.28l-1.06 1.06a.75.75 0 11-1.06-1.06L8.16 6.22l-1.06-1.06a.75.75 0 111.06-1.06L9.22 5.16l1.06-1.06a.75.75 0 010 1.12z" clip-rule="evenodd"/>
              </svg>
              Passwords don't match
            </span>
            <span v-else class="match">
              <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
                <path fill-rule="evenodd" d="M12.78 3.22a.75.75 0 00-1.06 0L7 7.94 4.28 5.22a.75.75 0 00-1.06 1.06l3.25 3.25a.75.75 0 001.06 0l5.5-5.5a.75.75 0 000-1.06z" clip-rule="evenodd"/>
              </svg>
              Passwords match
            </span>
          </div>
        </div>

        <div class="error-message" v-if="error">
          <svg width="18" height="18" viewBox="0 0 18 18" fill="currentColor">
            <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
          </svg>
          <span>{{ error }}</span>
        </div>

        <button 
          type="submit" 
          class="submit-btn"
          :disabled="!canSubmit || authStore.loading"
        >
          <span v-if="authStore.loading" class="btn-content">
            <span class="spinner-small"></span>
            Creating Identity...
          </span>
          <span v-else class="btn-content">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M10 3a1 1 0 011 1v5h5a1 1 0 110 2h-5v5a1 1 0 11-2 0v-5H4a1 1 0 110-2h5V4a1 1 0 011-1z"/>
            </svg>
            Create Identity
          </span>
        </button>
      </form>

      <div class="card-footer">
        <div class="security-note">
          <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
            <path fill-rule="evenodd" d="M8 1a4 4 0 00-4 4v2H3a1 1 0 00-1 1v8a1 1 0 001 1h10a1 1 0 001-1V8a1 1 0 00-1-1h-1V5a4 4 0 00-4-4zm2 5H6V5a2 2 0 114 0v1z" clip-rule="evenodd"/>
          </svg>
          <span>Your password never leaves this device</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.setup {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  position: relative;
}

.setup-backdrop {
  position: absolute;
  inset: 0;
  background: 
    radial-gradient(ellipse 60% 40% at 50% 0%, var(--primary-glow) 0%, transparent 50%),
    var(--bg-base);
}

.setup-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 420px;
  background: var(--bg-surface);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-8);
  box-shadow: var(--shadow-lg);
  animation: slideUp 0.4s ease-out;
}

@keyframes slideUp {
  from { opacity: 0; transform: translateY(20px); }
  to { opacity: 1; transform: translateY(0); }
}

.back-btn {
  position: absolute;
  top: var(--space-4);
  left: var(--space-4);
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  color: var(--text-muted);
  background: var(--bg-elevated);
  border-radius: var(--radius-md);
  transition: all var(--transition-fast);
}

.back-btn:hover {
  color: var(--text-primary);
  background: var(--bg-overlay);
}

.card-header {
  text-align: center;
  margin-bottom: var(--space-8);
}

.icon-wrapper {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  background: linear-gradient(135deg, var(--primary) 0%, var(--primary-muted) 100%);
  border-radius: var(--radius-lg);
  color: white;
  margin-bottom: var(--space-4);
}

.card-header h1 {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin-bottom: var(--space-2);
}

.card-header p {
  font-size: var(--text-sm);
  color: var(--text-secondary);
  line-height: var(--leading-relaxed);
}

.form {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.input-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.input-group label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-secondary);
}

.input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.input-wrapper input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  padding-right: 44px;
  background: var(--bg-base);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: var(--text-base);
  transition: all var(--transition-fast);
}

.input-wrapper input::placeholder {
  color: var(--text-muted);
}

.input-wrapper input:focus {
  outline: none;
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.input-wrapper input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.toggle-btn {
  position: absolute;
  right: var(--space-2);
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  color: var(--text-muted);
  border-radius: var(--radius-sm);
  transition: color var(--transition-fast);
}

.toggle-btn:hover {
  color: var(--text-primary);
}

.strength-meter {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.strength-bars {
  display: flex;
  gap: 4px;
  flex: 1;
}

.strength-bar {
  height: 4px;
  flex: 1;
  background: var(--border-default);
  border-radius: var(--radius-full);
  transition: background-color var(--transition-fast);
}

.strength-label {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  min-width: 50px;
}

.requirements {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--bg-base);
  border-radius: var(--radius-md);
}

.requirement {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
  transition: color var(--transition-fast);
}

.requirement.met {
  color: var(--success);
}

.confirm-status {
  font-size: var(--text-xs);
  display: flex;
  align-items: center;
}

.confirm-status .mismatch {
  color: var(--error);
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.confirm-status .match {
  color: var(--success);
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.error-message {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3);
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.2);
  border-radius: var(--radius-md);
  color: var(--error);
  font-size: var(--text-sm);
}

.submit-btn {
  width: 100%;
  padding: var(--space-4);
  background: var(--primary);
  color: white;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  border-radius: var(--radius-md);
  transition: all var(--transition-base);
  margin-top: var(--space-2);
}

.submit-btn:hover:not(:disabled) {
  background: var(--primary-hover);
  transform: translateY(-1px);
  box-shadow: var(--shadow-glow);
}

.submit-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.btn-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
}

.spinner-small {
  width: 18px;
  height: 18px;
  border: 2px solid rgba(255, 255, 255, 0.3);
  border-top-color: white;
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.card-footer {
  margin-top: var(--space-6);
  padding-top: var(--space-6);
  border-top: 1px solid var(--border-default);
}

.security-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
}
</style>
