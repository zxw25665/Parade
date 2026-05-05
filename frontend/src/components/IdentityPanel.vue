<template>
  <div class="panel">
    <h2>Identity</h2>

    <div v-if="store.hasIdentity === null" class="row">Checking...</div>

    <div v-else-if="!store.hasIdentity">
      <div class="row">
        <input v-model="password" type="password" placeholder="Set password" @keyup.enter="doRegister" />
        <button @click="doRegister" :disabled="!password || loading">Register</button>
      </div>
    </div>

    <div v-else-if="!store.loggedIn">
      <div class="row">
        <input v-model="password" type="password" placeholder="Enter password" @keyup.enter="doLogin" />
        <button @click="doLogin" :disabled="!password || loading">Login</button>
      </div>
    </div>

    <div v-else>
      <div class="badge badge-green" style="display:inline-block;margin-bottom:8px">Logged In</div>
      <div style="font-size:12px;word-break:break-all">PubKey: {{ store.pubkey }}</div>
    </div>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
  </div>
</template>

<script setup>
import { ref, onMounted, inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { checkHasIdentity, register, login } = useBackend()
const store = inject('store')

const password = ref('')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

onMounted(async () => {
  if (store.hasIdentity !== null) return
  try {
    store.hasIdentity = await checkHasIdentity()
  } catch (e) {
    errorMsg.value = e.toString()
  }
})

async function doRegister() {
  loading.value = true; errorMsg.value = ''; successMsg.value = ''
  try {
    await register(password.value)
    successMsg.value = 'Identity created. Now login.'
    store.hasIdentity = true
    password.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doLogin() {
  loading.value = true; errorMsg.value = ''; successMsg.value = ''
  try {
    await login(password.value)
    store.loggedIn = true
    store.pubkey = 'connected'
    successMsg.value = 'Logged in successfully'
    password.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}
</script>
