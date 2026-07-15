import { computed } from 'vue'
import { usePeersStore } from '@/stores/peers'
import type { ConnectResult } from '@/lib/types'

export function usePeers() {
  const peersStore = usePeersStore()

  const peers = computed(() => peersStore.peers)
  const savedPeers = computed(() => peersStore.savedPeers)
  const onlinePeers = computed(() => peersStore.onlinePeers)
  const offlinePeers = computed(() => peersStore.offlinePeers)
  const connectingPeers = computed(() => peersStore.connectingPeers)
  const peerCount = computed(() => peersStore.peerCount)
  const onlinePeerCount = computed(() => peersStore.onlinePeerCount)
  const isConnecting = computed(() => peersStore.isConnecting)
  const loading = computed(() => peersStore.loading)
  const error = computed(() => peersStore.error)

  async function loadPeers() {
    await peersStore.loadPeers()
  }

  async function connectToPeer(ipAddress: string): Promise<ConnectResult> {
    return await peersStore.connectToPeer(ipAddress)
  }

  async function onForeground() {
    await peersStore.onForeground()
  }

  function savePeer(ip: string, alias?: string) {
    peersStore.savePeer(ip, alias)
  }

  function removeSavedPeer(ip: string) {
    peersStore.removeSavedPeer(ip)
  }

  function updateSavedPeerAlias(ip: string, alias: string) {
    peersStore.updateSavedPeerAlias(ip, alias)
  }

  function handlePeerJoined(payload: { peer_uuid: string; ip_address: string }) {
    peersStore.handlePeerJoined(payload)
  }

  function handlePeerLeft(payload: { peer_uuid: string; ip_address: string }) {
    peersStore.handlePeerLeft(payload)
  }

  function handlePeerStatus(payload: { uuid: string; status: 'online' | 'offline' }) {
    peersStore.handlePeerStatus(payload)
  }

  function getPeerByPubkey(pubkey: string) {
    return peersStore.getPeerByPubkey(pubkey)
  }

  function getPeerByIP(ip: string) {
    return peersStore.getPeerByIP(ip)
  }

  function isPeerConnecting(ipOrPubkey: string) {
    return peersStore.isPeerConnecting(ipOrPubkey)
  }

  return {
    peers,
    savedPeers,
    onlinePeers,
    offlinePeers,
    connectingPeers,
    peerCount,
    onlinePeerCount,
    isConnecting,
    loading,
    error,
    loadPeers,
    connectToPeer,
    onForeground,
    savePeer,
    removeSavedPeer,
    updateSavedPeerAlias,
    handlePeerJoined,
    handlePeerLeft,
    handlePeerStatus,
    getPeerByPubkey,
    getPeerByIP,
    isPeerConnecting,
    clearError: peersStore.clearError,
    reset: peersStore.reset,
  }
}
