import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

export function useAuth() {
  const authStore = useAuthStore()
  const router = useRouter()

  const isAuthenticated = computed(() => authStore.isAuthenticated)
  const isLoggedIn = computed(() => authStore.isLoggedIn)
  const hasIdentity = computed(() => authStore.hasIdentity)
  const currentTeam = computed(() => authStore.currentTeam)
  const teams = computed(() => authStore.teams)
  const pubKey = computed(() => authStore.pubKey)
  const loading = computed(() => authStore.loading)
  const error = computed(() => authStore.error)

  async function login(password: string) {
    await authStore.login(password)
    router.push('/chat')
  }

  async function logout() {
    await authStore.logout()
    router.push('/login')
  }

  async function register(password: string) {
    await authStore.register(password)
    await login(password)
  }

  async function joinTeam(secret: string) {
    await authStore.joinTeam(secret)
  }

  async function joinTeamWithName(name: string, secret: string) {
    await authStore.joinTeamWithName(name, secret)
  }

  async function switchTeam(teamID: string) {
    await authStore.switchTeam(teamID)
  }

  async function leaveTeam(teamID: string) {
    await authStore.leaveTeam(teamID)
  }

  return {
    isAuthenticated,
    isLoggedIn,
    hasIdentity,
    currentTeam,
    teams,
    pubKey,
    loading,
    error,
    login,
    logout,
    register,
    joinTeam,
    joinTeamWithName,
    switchTeam,
    leaveTeam,
    checkIdentity: authStore.checkIdentity,
    hydrate: authStore.hydrate,
    refreshTeams: authStore.refreshTeams,
    clearError: authStore.clearError,
  }
}
