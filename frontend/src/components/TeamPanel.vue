<template>
  <div class="panel">
    <h2>Team</h2>
    <div v-if="!store.loggedIn" class="error" style="margin-bottom:8px">You must login in Identity page first</div>
    <div v-else-if="!store.teamJoined">
      <div class="row">
        <input v-model="secret" type="password" placeholder="Team secret / password" @keyup.enter="doJoin" />
        <button @click="doJoin" :disabled="!secret || loading">Join Team</button>
      </div>
    </div>
    <div v-else>
      <div class="badge badge-green">Team Joined · Network Active</div>
    </div>
    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
  </div>
</template>

<script setup>
import { ref, inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { joinTeam } = useBackend()
const store = inject('store')

const secret = ref('')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

async function doJoin() {
  loading.value = true; errorMsg.value = ''; successMsg.value = ''
  try {
    await joinTeam(secret.value)
    store.teamJoined = true
    successMsg.value = 'Joined team. Network started on port 4327.'
    secret.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
