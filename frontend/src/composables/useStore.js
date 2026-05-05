import { reactive } from 'vue'

const store = reactive({
  hasIdentity: null,
  loggedIn: false,
  pubkey: '',
  teamJoined: false,
  peerTests: {}  // { "192.168.1.x": { ip, pubkey, phase1, phase2, phase3Send, phase3Recv, expanded } }
})

export function useStore() {
  return store
}
