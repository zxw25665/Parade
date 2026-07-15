<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import Button from '@/components/ui/Button.vue'
import Input from '@/components/ui/Input.vue'
import { ref } from 'vue'

const { t } = useI18n()
const authStore = useAuthStore()

const secret = ref('')
const teamName = ref('')
const showCreateModal = ref(false)
const loading = ref(false)
const error = ref('')

async function handleJoinTeam() {
  if (!secret.value.trim()) return
  loading.value = true
  error.value = ''
  try {
    await authStore.joinTeam(secret.value.trim())
    secret.value = ''
  } catch (e) {
    error.value = t('team.joinFailed')
  } finally {
    loading.value = false
  }
}

async function handleCreateTeam() {
  if (!teamName.value.trim() || !secret.value.trim()) return
  loading.value = true
  error.value = ''
  try {
    await authStore.joinTeamWithName(teamName.value.trim(), secret.value.trim())
    secret.value = ''
    teamName.value = ''
    showCreateModal.value = false
  } catch (e) {
    error.value = t('team.joinFailed')
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="team-view">
    <div class="team-header">
      <h2>{{ t('team.title') }}</h2>
    </div>

    <div class="team-content">
      <section class="current-team">
        <h3>{{ t('team.currentTeam') }}</h3>
        <div v-if="authStore.currentTeam" class="team-card">
          <div class="team-info">
            <span class="team-name">{{ authStore.currentTeam.name }}</span>
            <span class="team-hash">{{ authStore.currentTeam.team_hash.slice(0, 8) }}...</span>
          </div>
        </div>
        <div v-else class="empty-state">
          {{ t('team.noTeam') }}
        </div>
      </section>

      <section class="team-actions">
        <h3>{{ t('team.joinTeam') }}</h3>
        <form @submit.prevent="handleJoinTeam" class="join-form">
          <Input
            v-model="secret"
            :label="t('team.teamSecret')"
            :placeholder="t('team.enterSecret')"
            :error="error"
          />
          <Button type="submit" :loading="loading" :disabled="!secret.trim()">
            {{ t('team.joinTeam') }}
          </Button>
        </form>
      </section>

      <section class="your-teams">
        <h3>{{ t('team.yourTeams') }}</h3>
        <div v-if="authStore.teams.length === 0" class="empty-state">
          {{ t('common.noData') }}
        </div>
        <div v-else class="team-list">
          <div
            v-for="team in authStore.teams"
            :key="team.id"
            :class="['team-item', { active: team.id === authStore.currentTeam?.id }]"
          >
            <div class="team-info">
              <span class="team-name">{{ team.name }}</span>
              <span class="team-hash">{{ team.team_hash.slice(0, 8) }}...</span>
            </div>
            <Button
              v-if="team.id !== authStore.currentTeam?.id"
              size="sm"
              variant="secondary"
              @click="authStore.switchTeam(team.id)"
            >
              {{ t('team.switchTeam') }}
            </Button>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<style scoped>
.team-view {
  height: 100%;
  display: flex;
  flex-direction: column;
}

.team-header {
  padding: 1rem 1.5rem;
  border-bottom: 1px solid #374151;
}

.team-header h2 {
  margin: 0;
  font-size: 1.25rem;
  color: #f9fafb;
}

.team-content {
  flex: 1;
  overflow-y: auto;
  padding: 1.5rem;
  display: flex;
  flex-direction: column;
  gap: 2rem;
}

section h3 {
  margin: 0 0 1rem 0;
  font-size: 1rem;
  font-weight: 600;
  color: #d1d5db;
}

.team-card,
.team-item {
  padding: 1rem;
  background: #1f2937;
  border: 1px solid #374151;
  border-radius: 8px;
}

.team-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.team-item.active {
  border-color: #6366f1;
}

.team-info {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}

.team-name {
  font-weight: 500;
  color: #f9fafb;
}

.team-hash {
  font-size: 0.75rem;
  color: #6b7280;
  font-family: monospace;
}

.team-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.join-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
  max-width: 400px;
}

.empty-state {
  padding: 1.5rem;
  text-align: center;
  color: #6b7280;
  font-size: 0.875rem;
}
</style>
