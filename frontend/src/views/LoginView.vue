<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

const password = ref('')
const showPassword = ref(false)
const localError = ref('')

const canSubmit = computed(() => password.value.length > 0)

async function handleLogin() {
  if (!canSubmit.value) return
  
  localError.value = ''
  authStore.clearError()
  
  try {
    await authStore.login(password.value)
    
    // Use teams from hydrate() result, which login() calls internally
    const teams = authStore.teams
    await router.replace(teams.length > 0 ? '/chat' : '/team-join')
  } catch (e) {
    localError.value = e instanceof Error ? e.message : 'Failed to unlock identity'
  }
}
</script>

<template>
  <div class="login">
    <div class="login-backdrop"></div>
    
    <div class="login-card">
      <button class="back-btn" @click="router.push('/')" aria-label="Go back">
        <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M9.707 16.707a1 1 0 01-1.414 0l-6-6a1 1 0 010-1.414l6-6a1 1 0 011.414 1.414L5.414 9H17a1 1 0 110 2H5.414l4.293 4.293a1 1 0 010 1.414z" clip-rule="evenodd"/>
        </svg>
      </button>

      <div class="card-header">
        <div class="icon-wrapper">
          <svg width="32" height="32" viewBox="0 0 32 32" fill="none">
            <rect x="5" y="12" width="22" height="16" rx="3" stroke="currentColor" stroke-width="2"/>
            <path d="M11 12V8a5 5 0 0110 0v4" stroke="currentColor" stroke-width="2"/>
            <circle cx="16" cy="20" r="2" fill="currentColor"/>
            <path d="M16 22v3" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
          </svg>
        </div>
        <h1>Welcome Back</h1>
        <p>Enter your password to unlock your identity</p>
      </div>

      <form @submit.prevent="handleLogin" class="form">
        <div class="input-group">
          <label for="password">Password</label>
          <div class="input-wrapper">
            <input
              id="password"
              v-model="password"
              :type="showPassword ? 'text' : 'password'"
              placeholder="Enter your password"
              autocomplete="current-password"
              :disabled="authStore.loading"
              @keydown.enter="handleLogin"
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
        </div>

        <div class="error-message" v-if="localError || authStore.error">
          <svg width="18" height="18" viewBox="0 0 18 18" fill="currentColor">
            <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
          </svg>
          <span>{{ localError || authStore.error }}</span>
        </div>

        <button 
          type="submit" 
          class="submit-btn"
          :disabled="!canSubmit || authStore.loading"
        >
          <span v-if="authStore.loading" class="btn-content">
            <span class="spinner-small"></span>
            Unlocking...
          </span>
          <span v-else class="btn-content">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path fill-rule="evenodd" d="M5 9V7a5 5 0 0110 0v2a2 2 0 012 2v5a2 2 0 01-2 2H5a2 2 0 01-2-2v-5a2 2 0 012-2zm8-2v2H7V7a3 3 0 016 0z" clip-rule="evenodd"/>
            </svg>
            Unlock Identity
          </span>
        </button>
      </form>

      <div class="card-footer">
        <p class="forgot-note">
          <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
            <path fill-rule="evenodd" d="M7 1a6 6 0 100 12A6 6 0 007 1zM0 7a7 7 0 1114 0A7 7 0 010 7z" clip-rule="evenodd"/>
            <path d="M7 6.25a.75.75 0 01.75.75v3.69a.75.75 0 01-1.5 0V7a.75.75 0 01.75-.75zM7 4a1 1 0 100 2 1 1 0 000-2z"/>
          </svg>
          Forgot your password? You'll need to reset your identity.
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  position: relative;
}

.login-backdrop {
  position: absolute;
  inset: 0;
  background: 
    radial-gradient(ellipse 60% 40% at 50% 0%, var(--primary-glow) 0%, transparent 50%),
    var(--bg-base);
}

.login-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 400px;
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
  background: linear-gradient(135deg, var(--accent) 0%, var(--accent-muted) 100%);
  border-radius: var(--radius-lg);
  color: var(--bg-base);
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

.forgot-note {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
  text-align: center;
}
</style>
