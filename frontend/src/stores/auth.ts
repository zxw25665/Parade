import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ParadeRPC } from '@/lib/rpc-client'
import type { Team } from '@/lib/types'

export const useAuthStore = defineStore('auth', () => {
  const isLoggedIn = ref(false)
  const hasIdentity = ref(false)
  const currentTeam = ref<Team | null>(null)
  const teams = ref<Team[]>([])
  const pubKey = ref('')
  const loading = ref(false)
  const error = ref<string | null>(null)

  let rpc: ParadeRPC | null = null

  const isAuthenticated = computed(() => isLoggedIn.value && hasIdentity.value)
  const currentTeamId = computed(() => currentTeam.value?.id ?? null)
  const currentTeamName = computed(() => currentTeam.value?.name ?? null)

  function setRPC(rpcInstance: ParadeRPC) {
    rpc = rpcInstance
  }

  async function waitForRPC(): Promise<ParadeRPC> {
    for (let i = 0; i < 20; i++) {
      if (rpc && rpc.getState() === 'connected') return rpc
      await new Promise(r => setTimeout(r, 200))
    }
    throw new Error('RPC connection timeout')
  }

  async function checkIdentity(): Promise<boolean> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null

    // Poll until daemon is ready (it spawns on a background thread).
    // Once connected, the call returns immediately on subsequent attempts.
    const start = Date.now()
    const daemonTimeout = 30000
    while (true) {
      try {
        hasIdentity.value = await r.checkHasIdentity()
        loading.value = false
        return hasIdentity.value
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e)
        if (msg.includes('Daemon not connected') && (Date.now() - start) < daemonTimeout) {
          await new Promise(r => setTimeout(r, 500))
          continue
        }
        error.value = msg || 'Failed to check identity'
        loading.value = false
        throw e
      }
    }
  }

  async function register(password: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.register(password)
      hasIdentity.value = true
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to register'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function login(password: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.login(password)
      isLoggedIn.value = true
      await hydrate()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to login'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function logout(): Promise<void> {
    isLoggedIn.value = false
    currentTeam.value = null
    teams.value = []
    pubKey.value = ''
  }

  async function hydrate(): Promise<void> {
    if (!rpc || !isLoggedIn.value) return
    loading.value = true
    error.value = null
    try {
      const [teamsResult, activeTeamId, pubKeyResult] = await Promise.all([
        rpc.listTeams(),
        rpc.getActiveTeam(),
        rpc.getPubKey(),
      ])
      teams.value = teamsResult
      pubKey.value = pubKeyResult
      if (activeTeamId) {
        currentTeam.value = teams.value.find(t => t.id === activeTeamId) ?? null
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to hydrate auth state'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function refreshTeams(): Promise<void> {
    const r = await waitForRPC()
    try {
      teams.value = await r.listTeams()
      const activeTeamId = await r.getActiveTeam()
      if (activeTeamId) {
        currentTeam.value = teams.value.find(t => t.id === activeTeamId) ?? null
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to refresh teams'
    }
  }

  async function joinTeam(secret: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.joinTeam(secret)
      await refreshTeams()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to join team'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function joinTeamWithName(name: string, secret: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.joinTeamWithName(name, secret)
      await refreshTeams()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to join team'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function leaveTeam(teamID: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.leaveTeam(teamID)
      await refreshTeams()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to leave team'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function switchTeam(teamID: string): Promise<void> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null
    try {
      await r.switchTeam(teamID)
      currentTeam.value = teams.value.find(t => t.id === teamID) ?? null
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to switch team'
      throw e
    } finally {
      loading.value = false
    }
  }

  function clearError(): void {
    error.value = null
  }

  return {
    isLoggedIn,
    hasIdentity,
    currentTeam,
    teams,
    pubKey,
    loading,
    error,
    isAuthenticated,
    currentTeamId,
    currentTeamName,
    setRPC,
    checkIdentity,
    register,
    login,
    logout,
    hydrate,
    refreshTeams,
    joinTeam,
    joinTeamWithName,
    leaveTeam,
    switchTeam,
    clearError,
  }
})
