<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const authStore = useAuthStore()

const activeTab = ref<'join' | 'create'>('join')

const teamName = ref('')
const teamSecret = ref('')
const localError = ref('')
const copied = ref(false)

onMounted(async () => {
  await authStore.refreshTeams()
})

const canJoin = computed(() => teamSecret.value.trim().length > 0)
const canCreate = computed(() => teamName.value.trim().length > 0 && teamSecret.value.trim().length > 0)

function generateSecret() {
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789'
  let result = ''
  for (let i = 0; i < 32; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length))
  }
  teamSecret.value = result
}

async function copySecret() {
  try {
    await navigator.clipboard.writeText(teamSecret.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    localError.value = 'Failed to copy to clipboard'
  }
}

async function handleJoin() {
  if (!canJoin.value) return
  
  localError.value = ''
  authStore.clearError()
  
  try {
    if (teamName.value.trim()) {
      await authStore.joinTeamWithName(teamName.value.trim(), teamSecret.value.trim())
    } else {
      await authStore.joinTeam(teamSecret.value.trim())
    }
    router.replace('/chat')
  } catch (e) {
    localError.value = e instanceof Error ? e.message : 'Failed to join team'
  }
}

async function handleCreate() {
  if (!canCreate.value) return
  
  localError.value = ''
  authStore.clearError()
  
  try {
    await authStore.joinTeamWithName(teamName.value.trim(), teamSecret.value.trim())
    router.replace('/chat')
  } catch (e) {
    localError.value = e instanceof Error ? e.message : 'Failed to create team'
  }
}
</script>

<template>
  <div class="team-join">
    <div class="team-join-backdrop"></div>
    
    <div class="team-join-card">
      <button class="back-btn" @click="router.push('/chat')" aria-label="Go back" v-if="authStore.teams.length > 0">
        <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
          <path fill-rule="evenodd" d="M9.707 16.707a1 1 0 01-1.414 0l-6-6a1 1 0 010-1.414l6-6a1 1 0 011.414 1.414L5.414 9H17a1 1 0 110 2H5.414l4.293 4.293a1 1 0 010 1.414z" clip-rule="evenodd"/>
        </svg>
      </button>

      <div class="card-header">
        <div class="icon-wrapper">
          <svg width="32" height="32" viewBox="0 0 32 32" fill="none">
            <circle cx="10" cy="12" r="4" stroke="currentColor" stroke-width="2"/>
            <circle cx="22" cy="12" r="4" stroke="currentColor" stroke-width="2"/>
            <circle cx="16" cy="22" r="4" stroke="currentColor" stroke-width="2"/>
            <path d="M10 16v4M22 16v4M6 12h2M24 12h2" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
          </svg>
        </div>
        <h1>Join a Team</h1>
        <p>Create a new team or join an existing one with a secret key</p>
      </div>

      <div class="tabs">
        <button 
          class="tab" 
          :class="{ active: activeTab === 'join' }"
          @click="activeTab = 'join'"
        >
          <svg width="18" height="18" viewBox="0 0 18 18" fill="currentColor">
            <path d="M11.25 6.75a.75.75 0 000 1.5h3a.75.75 0 000-1.5h-3z"/>
            <path fill-rule="evenodd" d="M9 1.5H3a1.5 1.5 0 00-1.5 1.5v12a1.5 1.5 0 001.5 1.5h12a1.5 1.5 0 001.5-1.5V9.75a.75.75 0 00-1.5 0v3.75a.75.75 0 01-.75.75H3a.75.75 0 01-.75-.75v-12a.75.75 0 01.75-.75h6a.75.75 0 000-1.5H3A3 3 0 000 3v12a3 3 0 003 3h6a.75.75 0 000-1.5H3a.75.75 0 01-.75-.75v-12a.75.75 0 01.75-.75h6a.75.75 0 000-1.5H3a3 3 0 00-3 3v12a3 3 0 003 3h6a3 3 0 003-3v-3a.75.75 0 00-1.5 0v3a1.5 1.5 0 01-1.5 1.5H3a1.5 1.5 0 01-1.5-1.5v-12A1.5 1.5 0 013 3h6a1.5 1.5 0 011.5 1.5.75.75 0 101.5 0A1.5 1.5 0 0115 3h3a1.5 1.5 0 011.5 1.5v12a1.5 1.5 0 01-1.5 1.5H15a.75.75 0 000 1.5h.75a3 3 0 003-3v-12a3 3 0 00-3-3h-1.5z" clip-rule="evenodd"/>
          </svg>
          Join Team
        </button>
        <button 
          class="tab" 
          :class="{ active: activeTab === 'create' }"
          @click="activeTab = 'create'"
        >
          <svg width="18" height="18" viewBox="0 0 18 18" fill="currentColor">
            <path d="M9 3.75a.75.75 0 00-.75.75v4.5h-4.5a.75.75 0 000 1.5h4.5v4.5a.75.75 0 001.5 0v-4.5h4.5a.75.75 0 000-1.5h-4.5v-4.5A.75.75 0 009 3.75z"/>
          </svg>
          Create Team
        </button>
      </div>

      <div class="tab-content">
        <div v-if="activeTab === 'join'" class="form-panel">
          <div class="input-group">
            <label for="join-name">Team Name (optional)</label>
            <input
              id="join-name"
              v-model="teamName"
              type="text"
              placeholder="My Awesome Team"
              :disabled="authStore.loading"
            />
          </div>

          <div class="input-group">
            <label for="join-secret">Team Secret</label>
            <div class="secret-input-wrapper">
              <input
                id="join-secret"
                v-model="teamSecret"
                type="text"
                placeholder="Paste the team secret here"
                :disabled="authStore.loading"
              />
              <span class="secret-hint">
                <svg width="14" height="14" viewBox="0 0 14 14" fill="currentColor">
                  <path fill-rule="evenodd" d="M7 1a6 6 0 100 12A6 6 0 007 1zM0 7a7 7 0 1114 0A7 7 0 010 7z" clip-rule="evenodd"/>
                </svg>
                Required
              </span>
            </div>
          </div>
        </div>

        <div v-else class="form-panel">
          <div class="input-group">
            <label for="create-name">Team Name</label>
            <input
              id="create-name"
              v-model="teamName"
              type="text"
              placeholder="My Awesome Team"
              :disabled="authStore.loading"
            />
          </div>

          <div class="input-group">
            <label for="create-secret">Team Secret</label>
            <div class="secret-input-wrapper">
              <input
                id="create-secret"
                v-model="teamSecret"
                type="text"
                placeholder="Generate or enter a secret"
                :disabled="authStore.loading"
              />
            </div>
            <div class="secret-actions">
              <button type="button" class="btn btn-ghost" @click="generateSecret">
                <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                  <path fill-rule="evenodd" d="M4 2a1 1 0 011 1v2.101a7.002 7.002 0 0111.601 2.566 1 1 0 11-1.885.666A5.002 5.002 0 005.999 7H9a1 1 0 010 2H4a1 1 0 01-1-1V3a1 1 0 011-1zm.008 9.057a1 1 0 011.276.61A5.002 5.002 0 0014.001 13H11a1 1 0 110-2h5a1 1 0 011 1v5a1 1 0 11-2 0v-2.101a7.002 7.002 0 01-11.601-2.566 1 1 0 01.61-1.276z" clip-rule="evenodd"/>
                </svg>
                Generate
              </button>
              <button 
                type="button" 
                class="btn btn-ghost" 
                @click="copySecret"
                :disabled="!teamSecret"
              >
                <svg v-if="!copied" width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                  <path d="M0 6.75C0 5.784.784 5 1.75 5h1.5a.75.75 0 010 1.5h-1.5a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-1.5a.75.75 0 011.5 0v1.5A1.75 1.75 0 019.25 16h-7.5A1.75 1.75 0 010 14.25v-7.5z"/>
                  <path d="M5 1.75C5 .784 5.784 0 6.75 0h7.5C15.216 0 16 .784 16 1.75v7.5A1.75 1.75 0 0114.25 11h-7.5A1.75 1.75 0 015 9.25v-7.5zm1.75-.25a.25.25 0 00-.25.25v7.5c0 .138.112.25.25.25h7.5a.25.25 0 00.25-.25v-7.5a.25.25 0 00-.25-.25h-7.5z"/>
                </svg>
                <svg v-else width="16" height="16" viewBox="0 0 16 16" fill="currentColor">
                  <path fill-rule="evenodd" d="M13.78 4.22a.75.75 0 010 1.06l-7.25 7.25a.75.75 0 01-1.06 0L2.22 9.28a.75.75 0 011.06-1.06L6 10.94l6.72-6.72a.75.75 0 011.06 0z" clip-rule="evenodd"/>
                </svg>
                {{ copied ? 'Copied!' : 'Copy' }}
              </button>
            </div>
            <p class="secret-help">
              Share this secret with others to let them join your team
            </p>
          </div>
        </div>

        <div class="error-message" v-if="localError || authStore.error">
          <svg width="18" height="18" viewBox="0 0 18 18" fill="currentColor">
            <path fill-rule="evenodd" d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z" clip-rule="evenodd"/>
          </svg>
          <span>{{ localError || authStore.error }}</span>
        </div>

        <button 
          v-if="activeTab === 'join'"
          class="submit-btn"
          :disabled="!canJoin || authStore.loading"
          @click="handleJoin"
        >
          <span v-if="authStore.loading" class="btn-content">
            <span class="spinner-small"></span>
            Joining...
          </span>
          <span v-else class="btn-content">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path d="M10 3a1 1 0 011 1v5h5a1 1 0 110 2h-5v5a1 1 0 11-2 0v-5H4a1 1 0 110-2h5V4a1 1 0 011-1z"/>
            </svg>
            Join Team
          </span>
        </button>

        <button 
          v-else
          class="submit-btn"
          :disabled="!canCreate || authStore.loading"
          @click="handleCreate"
        >
          <span v-if="authStore.loading" class="btn-content">
            <span class="spinner-small"></span>
            Creating...
          </span>
          <span v-else class="btn-content">
            <svg width="20" height="20" viewBox="0 0 20 20" fill="currentColor">
              <path fill-rule="evenodd" d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" clip-rule="evenodd"/>
            </svg>
            Create Team
          </span>
        </button>
      </div>

      <div class="existing-teams" v-if="authStore.teams.length > 0">
        <h3>Your Teams</h3>
        <div class="teams-list">
          <div 
            v-for="team in authStore.teams" 
            :key="team.id"
            class="team-item"
          >
            <div class="team-avatar">
              {{ team.name.charAt(0).toUpperCase() }}
            </div>
            <div class="team-info">
              <span class="team-name">{{ team.name }}</span>
              <span class="team-meta">
                {{ team.active ? 'Active' : 'Inactive' }}
              </span>
            </div>
            <button 
              class="btn btn-small btn-ghost"
              @click="authStore.switchTeam(team.id); router.push('/chat')"
            >
              Open
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.team-join {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  position: relative;
  overflow-y: auto;
}

.team-join-backdrop {
  position: absolute;
  inset: 0;
  background: 
    radial-gradient(ellipse 60% 40% at 50% 0%, var(--primary-glow) 0%, transparent 50%),
    var(--bg-base);
}

.team-join-card {
  position: relative;
  z-index: 1;
  width: 100%;
  max-width: 480px;
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
  margin-bottom: var(--space-6);
}

.icon-wrapper {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 64px;
  height: 64px;
  background: linear-gradient(135deg, var(--success) 0%, var(--success-muted) 100%);
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

.tabs {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-1);
  background: var(--bg-base);
  border-radius: var(--radius-md);
  margin-bottom: var(--space-6);
}

.tab {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-3);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-muted);
  background: transparent;
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.tab:hover {
  color: var(--text-secondary);
}

.tab.active {
  background: var(--bg-elevated);
  color: var(--text-primary);
  box-shadow: var(--shadow-sm);
}

.tab-content {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.form-panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
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

.input-group input {
  width: 100%;
  padding: var(--space-3) var(--space-4);
  background: var(--bg-base);
  border: 1px solid var(--border-default);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  font-size: var(--text-base);
  transition: all var(--transition-fast);
}

.input-group input::placeholder {
  color: var(--text-muted);
}

.input-group input:focus {
  outline: none;
  border-color: var(--primary);
  box-shadow: 0 0 0 3px var(--primary-glow);
}

.input-group input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.secret-input-wrapper {
  position: relative;
}

.secret-input-wrapper input {
  padding-right: 80px;
}

.secret-hint {
  position: absolute;
  right: var(--space-3);
  top: 50%;
  transform: translateY(-50%);
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  color: var(--error);
}

.secret-actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-1);
  padding: var(--space-2) var(--space-3);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  border-radius: var(--radius-sm);
  transition: all var(--transition-fast);
}

.btn-ghost {
  background: var(--bg-elevated);
  color: var(--text-secondary);
  border: 1px solid var(--border-default);
}

.btn-ghost:hover:not(:disabled) {
  background: var(--bg-overlay);
  color: var(--text-primary);
  border-color: var(--border-hover);
}

.btn-small {
  padding: var(--space-1) var(--space-2);
  font-size: var(--text-xs);
}

.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.secret-help {
  font-size: var(--text-xs);
  color: var(--text-muted);
  margin-top: var(--space-2);
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

.existing-teams {
  margin-top: var(--space-8);
  padding-top: var(--space-6);
  border-top: 1px solid var(--border-default);
}

.existing-teams h3 {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--text-muted);
  margin-bottom: var(--space-4);
  text-transform: uppercase;
  letter-spacing: 0.05em;
}

.teams-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.team-item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  background: var(--bg-base);
  border-radius: var(--radius-md);
  transition: background var(--transition-fast);
}

.team-item:hover {
  background: var(--bg-elevated);
}

.team-avatar {
  width: 36px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(135deg, var(--primary) 0%, var(--accent) 100%);
  color: white;
  font-weight: var(--font-semibold);
  border-radius: var(--radius-md);
}

.team-info {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.team-name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--text-primary);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.team-meta {
  font-size: var(--text-xs);
  color: var(--text-muted);
}
</style>
