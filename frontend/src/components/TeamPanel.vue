<template>
    <div v-if="!store.loggedIn" class="error" style="margin-bottom:8px">You must login in Identity page first</div>
    <div v-else>
      <div class="row" style="margin-bottom:12px">
        <input v-model="teamName" placeholder="Team name (optional)" style="flex:1" />
        <input v-model="teamSecret" type="password" placeholder="Team secret" style="flex:1" @keyup.enter="doJoin" />
        <button @click="doJoin" :disabled="!teamSecret || loading">Join / Create Team</button>
      </div>

      <div v-if="store.teams.length > 0">
        <div style="font-size:13px;color:#8a8aaf;margin-bottom:8px">My Teams</div>
        <div class="list">
          <div v-for="team in store.teams" :key="team.id" class="list-item" style="justify-content:space-between">
            <div style="display:flex;align-items:center;gap:8px">
              <span style="font-weight:500">{{ team.name || 'Unnamed Team' }}</span>
              <span v-if="team.active" class="badge badge-green">Active</span>
            </div>
            <div style="display:flex;gap:6px">
              <button v-if="!team.active" @click="doSwitch(team.id)" :disabled="loading" style="font-size:12px;padding:4px 10px">Switch</button>
              <button @click="doLeave(team.id)" :disabled="loading" style="font-size:12px;padding:4px 10px;background:#e94560">Leave</button>
            </div>
          </div>
        </div>
      </div>
      <div v-else style="font-size:13px;color:#8a8aaf">
        No teams joined yet
      </div>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

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
    errorMsg.value = 'Failed to load teams: ' + e.toString()
  }
}

async function doJoin() {
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    await joinTeamWithName(teamName.value, teamSecret.value)
    successMsg.value = 'Joined team successfully'
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
    successMsg.value = 'Switched team'
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
    successMsg.value = 'Left team'
    await refreshTeams()
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
