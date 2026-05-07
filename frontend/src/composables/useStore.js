import { reactive } from 'vue'

const store = reactive({
  hasIdentity: null,
  loggedIn: false,
  pubkey: '',
  teamJoined: false,
  teams: [],
  activeTeamId: '',
  channels: [],
  activeChannelId: '',
  peerTests: {}
})

export function useStore() {
  return store
}
