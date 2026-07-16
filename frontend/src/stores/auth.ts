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
    if (!rpc) throw new Error('RPC not injected')
    const deadline = Date.now() + 65_000
    while (Date.now() < deadline) {
      if (rpc.getState() === 'connected') return rpc
      await new Promise(r => setTimeout(r, 200))
    }
    throw new Error('Daemon did not start in time (65s timeout)')
  }

  async function checkIdentity(): Promise<boolean> {
    const r = await waitForRPC()
    loading.value = true
    error.value = null

    const deadline = Date.now() + 30_000
    let attempts = 0
    while (Date.now() < deadline && attempts < 150) {
      attempts++
      try {
        hasIdentity.value = await r.checkHasIdentity()
        loading.value = false
        return hasIdentity.value
      } catch (e) {
        const msg = e instanceof Error ? e.message : String(e)
        // Retry on any connection-related error during daemon startup window
        if ((msg.includes('Not connected') ||
             msg.includes('timeout') ||
             msg.includes('Daemon not connected') ||
             msg.includes('RPC not initialized') ||
             msg.includes('Timed out')) &&
            (Date.now() - deadline + 30_000) < 30_000) {
          await new Promise(r => setTimeout(r, 200))
          continue
        }
        error.value = msg || 'Failed to check identity'
        loading.value = false
        throw e
      }
    }
    error.value = 'Identity check timed out after 30s'
    loading.value = false
    throw new Error('Identity check timed out')
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
    try {
      if (rpc && rpc.getState() === 'connected') {
        await rpc.logout()
      }
    } catch {
      // Ignore — daemon may already be down
    }
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
