<template>
    <div class="team-panel-root">
      <div class="row" style="margin-bottom:4px">
        <input v-model="teamName" :placeholder="$t('team.teamNameOptional')" style="flex:1" />
        <input v-model="teamSecret" type="password" :placeholder="$t('team.teamSecret')" style="flex:1" @keyup.enter="doJoin" />
        <button class="btn-sm" @click="doJoin" :disabled="!teamSecret.trim() || loading">{{ $t('team.joinCreate') }}</button>
      </div>

      <div v-if="store.teams.length > 0" class="list" style="max-height:100px;overflow-y:auto">
        <div v-for="team in store.teams" :key="team.id" class="list-item" style="justify-content:space-between;align-items:center;padding:4px 6px;">
          <span style="font-weight:500;font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1">{{ team.name || $t('team.unnamedTeam') }}</span>
          <span v-if="team.active" class="badge badge-green" style="font-size:10px;padding:1px 6px">{{ $t('team.active') }}</span>
          <div style="display:flex;gap:3px;flex-shrink:0">
            <button v-if="!team.active" class="btn-sm" style="font-size:10px;padding:2px 6px" @click="doSwitch(team.id)" :disabled="loading">{{ $t('team.switch') }}</button>
            <button class="btn-sm" style="font-size:10px;padding:2px 6px;background:var(--color-danger)" @click="doLeave(team.id)" :disabled="loading">{{ $t('team.leave') }}</button>
          </div>
        </div>
      </div>

      <div v-if="errorMsg" class="error" style="font-size:11px">{{ errorMsg }}</div>
    </div>
</template>

<script setup>
import { ref, onMounted, inject, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'

const { t } = useI18n()
const { joinTeamWithName, leaveTeam, switchTeam, listTeams, getActiveTeam } = useBackend()
const store = inject('store')

const teamName = ref('')
const teamSecret = ref('')
const loading = ref(false)
const errorMsg = ref('')

onMounted(async () => {
  if (store.loggedIn) await refreshTeams()
})

watch(() => store.loggedIn, (newVal) => {
  if (newVal) refreshTeams()
})

async function refreshTeams() {
  try {
    const teams = await listTeams()
    store.teams = teams || []
    store.teamJoined = store.teams.length > 0
    const activeId = await getActiveTeam()
    store.activeTeamId = activeId || ''
    store.teams.forEach(t => { t.active = t.id === store.activeTeamId })
  } catch (e) {
    errorMsg.value = e.toString()
  }
}

async function doJoin() {
  const s = teamSecret.value.trim()
  if (!s) return
  loading.value = true; errorMsg.value = ''
  try {
    await joinTeamWithName(teamName.value, s)
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
  loading.value = true; errorMsg.value = ''
  try { await switchTeam(teamId); await refreshTeams() } catch (e) { errorMsg.value = e.toString() }
  finally { loading.value = false }
}

async function doLeave(teamId) {
  loading.value = true; errorMsg.value = ''
  try { await leaveTeam(teamId); await refreshTeams(); if (store.teams.length === 0) store.teamJoined = false } catch (e) { errorMsg.value = e.toString() }
  finally { loading.value = false }
}
</script>
