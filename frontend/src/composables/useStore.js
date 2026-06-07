import { reactive } from 'vue'

const store = reactive({
  hasIdentity: null,
  loggedIn: false,
  pubkey: '',
  teamJoined: false,
  teams: [],
  activeTeamId: '',
  peerTests: {},

  conversations: [],
  activeConversationId: '',
  peersWithStatus: [],
  messagesByConv: {}
})

export function useStore() {
  return store
}
