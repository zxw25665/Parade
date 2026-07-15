<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

const loading = ref(true)
const error = ref('')

onMounted(async () => {
  try {
    await authStore.checkIdentity()
    
    if (authStore.isLoggedIn && authStore.currentTeam) {
      router.replace('/chat')
    } else if (authStore.hasIdentity) {
      router.replace('/login')
    } else {
      router.replace('/setup')
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to check identity'
  } finally {
    loading.value = false
  }
})

const statusMessage = computed(() => {
  if (error.value) return error.value
  if (loading.value) return 'Connecting to Parade...'
  return ''
})
</script>

<template>
  <div class="home">
    <div class="home-backdrop"></div>
    <div class="home-glow"></div>
    
    <div class="home-content">
      <div class="logo-container">
        <div class="logo-icon">
          <svg width="48" height="48" viewBox="0 0 48 48" fill="none">
            <path d="M24 4L42 14V34L24 44L6 34V14L24 4Z" stroke="currentColor" stroke-width="2" fill="none"/>
            <path d="M24 14L34 20V32L24 38L14 32V20L24 14Z" fill="currentColor" opacity="0.3"/>
            <circle cx="24" cy="24" r="4" fill="currentColor"/>
          </svg>
        </div>
        <h1 class="logo-text">Parade</h1>
      </div>

      <p class="tagline">Decentralized P2P Collaboration</p>
      <p class="sub-tagline">End-to-end encrypted · Serverless · LAN-first</p>

      <div class="loading-indicator" v-if="loading">
        <div class="spinner"></div>
        <span class="loading-text">{{ statusMessage }}</span>
      </div>

      <div class="error-message" v-else-if="error">
        <svg class="error-icon" width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z" clip-rule="evenodd"/>
        </svg>
        <span>{{ error }}</span>
      </div>

      <div class="actions" v-else>
        <button 
          class="btn btn-primary" 
          @click="router.push('/setup')"
          v-if="!authStore.hasIdentity"
        >
          <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
            <path d="M10 3a1 1 0 011 1v5h5a1 1 0 110 2h-5v5a1 1 0 11-2 0v-5H4a1 1 0 110-2h5V4a1 1 0 011-1z"/>
          </svg>
          Create Identity
        </button>
        <button 
          class="btn btn-secondary" 
          @click="router.push('/login')"
          v-else
        >
          <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
            <path fill-rule="evenodd" d="M3 3a1 1 0 011 1v12a1 1 0 11-2 0V4a1 1 0 011-1zm7.707 3.293a1 1 0 010 1.414L9.414 9H17a1 1 0 110 2H9.414l1.293 1.293a1 1 0 01-1.414 1.414l-3-3a1 1 0 010-1.414l3-3a1 1 0 011.414 0z" clip-rule="evenodd"/>
          </svg>
          Unlock Identity
        </button>
      </div>
    </div>

    <div class="home-footer">
      <span class="version">v1.0.0</span>
      <span class="separator">·</span>
      <span class="tech">Built with libp2p</span>
    </div>
  </div>
</template>

<style scoped>
.home {
  width: 100%;
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  position: relative;
  overflow: hidden;
}

.home-backdrop {
  position: absolute;
  inset: 0;
  background: 
    radial-gradient(ellipse 80% 50% at 50% -20%, var(--primary-glow) 0%, transparent 50%),
    linear-gradient(180deg, var(--bg-surface) 0%, var(--bg-base) 100%);
}

.home-glow {
  position: absolute;
  top: 30%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 600px;
  height: 600px;
  background: radial-gradient(circle, var(--primary-glow) 0%, transparent 70%);
  opacity: 0.4;
  animation: pulse 4s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% { opacity: 0.3; transform: translate(-50%, -50%) scale(1); }
  50% { opacity: 0.5; transform: translate(-50%, -50%) scale(1.1); }
}

.home-content {
  position: relative;
  z-index: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  text-align: center;
  padding: var(--space-8);
  animation: fadeIn 0.6s ease-out;
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(20px); }
  to { opacity: 1; transform: translateY(0); }
}

.logo-container {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
  margin-bottom: var(--space-6);
}

.logo-icon {
  color: var(--primary);
  animation: float 3s ease-in-out infinite;
}

@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-8px); }
}

.logo-text {
  font-family: var(--font-display);
  font-size: var(--text-4xl);
  font-weight: var(--font-bold);
  letter-spacing: -0.02em;
  background: linear-gradient(135deg, var(--text-primary) 0%, var(--primary) 100%);
  -webkit-background-clip: text;
  -webkit-text-fill-color: transparent;
  background-clip: text;
}

.tagline {
  font-size: var(--text-xl);
  color: var(--text-secondary);
  margin-bottom: var(--space-2);
  font-weight: var(--font-medium);
}

.sub-tagline {
  font-size: var(--text-sm);
  color: var(--text-muted);
  margin-bottom: var(--space-10);
}

.loading-indicator {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-4);
}

.spinner {
  width: 32px;
  height: 32px;
  border: 3px solid var(--border-default);
  border-top-color: var(--primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.loading-text {
  font-size: var(--text-sm);
  color: var(--text-muted);
}

.error-message {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: rgba(239, 68, 68, 0.1);
  border: 1px solid rgba(239, 68, 68, 0.2);
  border-radius: var(--radius-md);
  color: var(--error);
  font-size: var(--text-sm);
  margin-top: var(--space-4);
}

.error-icon {
  flex-shrink: 0;
}

.actions {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  width: 100%;
  max-width: 280px;
  margin-top: var(--space-6);
  animation: fadeIn 0.6s ease-out 0.2s backwards;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-6);
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  border-radius: var(--radius-lg);
  transition: all var(--transition-base);
  position: relative;
  overflow: hidden;
}

.btn::before {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(135deg, transparent 0%, rgba(255,255,255,0.1) 50%, transparent 100%);
  transform: translateX(-100%);
  transition: transform var(--transition-slow);
}

.btn:hover::before {
  transform: translateX(100%);
}

.btn-primary {
  background: var(--primary);
  color: white;
  box-shadow: 0 4px 14px 0 var(--primary-glow);
}

.btn-primary:hover {
  background: var(--primary-hover);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px 0 var(--primary-glow);
}

.btn-primary:active {
  transform: translateY(0);
}

.btn-secondary {
  background: var(--bg-elevated);
  color: var(--text-primary);
  border: 1px solid var(--border-default);
}

.btn-secondary:hover {
  background: var(--bg-overlay);
  border-color: var(--border-hover);
  transform: translateY(-2px);
}

.home-footer {
  position: absolute;
  bottom: var(--space-6);
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--text-xs);
  color: var(--text-muted);
}

.separator {
  opacity: 0.5;
}
</style>
