<template>
    <div v-if="!store.loggedIn" class="error" style="margin-bottom:8px">{{ $t('team.mustLogin') }}</div>
    <div v-else>
      <div class="row" style="margin-bottom:12px">
        <input v-model="teamName" :placeholder="$t('team.teamNameOptional')" style="flex:1" />
        <input v-model="teamSecret" type="password" :placeholder="$t('team.teamSecret')" style="flex:1" @keyup.enter="doJoin" />
        <button @click="doJoin" :disabled="!teamSecret || loading">{{ $t('team.joinCreate') }}</button>
      </div>

      <div v-if="store.teams.length > 0">
        <div style="font-size:13px;color:#8a8aaf;margin-bottom:8px">{{ $t('team.myTeams') }}</div>
        <div class="list">
          <div v-for="team in store.teams" :key="team.id" class="list-item" style="justify-content:space-between">
            <div style="display:flex;align-items:center;gap:8px">
              <span style="font-weight:500">{{ team.name || $t('team.unnamedTeam') }}</span>
              <span v-if="team.active" class="badge badge-green">{{ $t('team.active') }}</span>
            </div>
            <div style="display:flex;gap:6px">
              <button v-if="!team.active" @click="doSwitch(team.id)" :disabled="loading" style="font-size:12px;padding:4px 10px">{{ $t('team.switch') }}</button>
              <button @click="doLeave(team.id)" :disabled="loading" style="font-size:12px;padding:4px 10px;background:#e94560">{{ $t('team.leave') }}</button>
            </div>
          </div>
        </div>
      </div>
      <div v-else style="font-size:13px;color:#8a8aaf">
        {{ $t('team.noTeams') }}
      </div>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'

const { t } = useI18n()
const { joinTeamWithName, leaveTeam, switchTeam, listTeams, getActiveTeam } = useBackend()
const store = inject('store')

const teamName = ref('')
const teamSecret = ref('')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

onMounted(async () => {
  if (!store.loggedIn) return
  await refreshTeams()
})

async function refreshTeams() {
  try {
    const teams = await listTeams()
    store.teams = teams || []
    store.teamJoined = store.teams.length > 0
    const activeId = await getActiveTeam()
    store.activeTeamId = activeId || ''
    store.teams.forEach(t => {
      t.active = t.id === store.activeTeamId
    })
  } catch (e) {
    errorMsg.value = t('team.loadTeamsFailed') + ': ' + e.toString()
  }
}

async function doJoin() {
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    await joinTeamWithName(teamName.value, teamSecret.value)
    successMsg.value = t('team.joinSuccess')
    teamName.value = ''
    teamSecret.value = ''
    await refreshTeams()
    store.teamJoined = true
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doSwitch(teamId) {
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    await switchTeam(teamId)
    successMsg.value = t('team.switchSuccess')
    await refreshTeams()
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doLeave(teamId) {
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    await leaveTeam(teamId)
    successMsg.value = t('team.leaveSuccess')
    await refreshTeams()
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
