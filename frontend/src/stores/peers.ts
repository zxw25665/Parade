import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ParadeRPC } from '@/lib/rpc-client'
import type { PeerWithStatus, ConnectResult } from '@/lib/types'

export interface SavedPeer {
  ip: string
  pubkey?: string
  alias?: string
  addedAt: string
}

export const usePeersStore = defineStore('peers', () => {
  const peers = ref<PeerWithStatus[]>([])
  const savedPeers = ref<SavedPeer[]>([])
  const loading = ref(false)
  const connecting = ref<Set<string>>(new Set())
  const error = ref<string | null>(null)

  let rpc: ParadeRPC | null = null

  const onlinePeers = computed(() => peers.value.filter(p => p.status === 'online'))
  const offlinePeers = computed(() => peers.value.filter(p => p.status === 'offline'))
  const connectingPeers = computed(() => peers.value.filter(p => p.status === 'connecting'))
  const peerCount = computed(() => peers.value.length)
  const onlinePeerCount = computed(() => onlinePeers.value.length)
  const isConnecting = computed(() => connecting.value.size > 0)

  const savedPeerIPs = computed(() => new Set(savedPeers.value.map(p => p.ip)))

  function isPeerSaved(ip: string): boolean {
    return savedPeerIPs.value.has(ip)
  }

  function setRPC(rpcInstance: ParadeRPC) {
    rpc = rpcInstance
  }

  async function loadPeers(): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      const [discoveredPeers, saved] = await Promise.all([
        rpc.getPeersWithStatus(),
        rpc.listSavedPeers(),
      ])
      peers.value = discoveredPeers
      savedPeers.value = saved.map(ip => ({
        ip,
        addedAt: new Date().toISOString(),
      }))
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load peers'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function connectToPeer(ipAddress: string): Promise<ConnectResult> {
    if (!rpc) throw new Error('RPC not initialized')
    connecting.value.add(ipAddress)
    error.value = null
    try {
      const result = await rpc.connectToPeer(ipAddress)
      const existingPeer = peers.value.find(p => p.ip === ipAddress)
      if (existingPeer) {
        existingPeer.status = result.phase2.success ? 'online' : 'offline'
        existingPeer.pubkey = result.pubkey
      } else if (result.phase2.success) {
        peers.value.push({
          pubkey: result.pubkey,
          ip: result.ip,
          status: 'online',
          last_heartbeat: new Date().toISOString(),
          last_online: new Date().toISOString(),
        })
      }
      return result
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to connect to peer'
      throw e
    } finally {
      connecting.value.delete(ipAddress)
    }
  }

  async function addSavedPeer(ip: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.savePeer(ip)
      if (!savedPeers.value.find(p => p.ip === ip)) {
        savedPeers.value.push({ ip, addedAt: new Date().toISOString() })
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to save peer'
      throw e
    }
  }

  async function removeSavedPeer(ip: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.removePeer(ip)
      savedPeers.value = savedPeers.value.filter(p => p.ip !== ip)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to remove peer'
      throw e
    }
  }

  async function savePeer(ip: string, alias?: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.savePeer(ip)
      const existing = savedPeers.value.find(p => p.ip === ip)
      if (existing) {
        if (alias) existing.alias = alias
      } else {
        savedPeers.value.push({ ip, alias, addedAt: new Date().toISOString() })
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to save peer'
      throw e
    }
  }

  function updateSavedPeerAlias(ip: string, alias: string): void {
    const peer = savedPeers.value.find(p => p.ip === ip)
    if (peer) peer.alias = alias
  }

  async function onForeground(): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      await rpc.onForeground()
      await loadPeers()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to handle foreground'
      throw e
    }
  }

  function handlePeerJoined(payload: { peer_uuid: string; ip_address: string }): void {
    const existingPeer = peers.value.find(p => p.pubkey === payload.peer_uuid)
    if (existingPeer) {
      existingPeer.status = 'online'
      existingPeer.last_heartbeat = new Date().toISOString()
      existingPeer.last_online = new Date().toISOString()
    } else {
      peers.value.push({
        pubkey: payload.peer_uuid,
        ip: payload.ip_address,
        status: 'online',
        last_heartbeat: new Date().toISOString(),
        last_online: new Date().toISOString(),
      })
    }
  }

  function handlePeerLeft(payload: { peer_uuid: string; ip_address: string }): void {
    const peer = peers.value.find(p => p.pubkey === payload.peer_uuid)
    if (peer) {
      peer.status = 'offline'
      peer.last_online = new Date().toISOString()
    }
  }

  function handlePeerStatus(payload: { uuid: string; status: 'online' | 'offline' }): void {
    const peer = peers.value.find(p => p.pubkey === payload.uuid)
    if (peer) {
      peer.status = payload.status
      if (payload.status === 'online') {
        peer.last_heartbeat = new Date().toISOString()
        peer.last_online = new Date().toISOString()
      }
    }
  }

  function getPeerByPubkey(pubkey: string): PeerWithStatus | undefined {
    return peers.value.find(p => p.pubkey === pubkey)
  }

  function getPeerByIP(ip: string): PeerWithStatus | undefined {
    return peers.value.find(p => p.ip === ip)
  }

  function isPeerConnecting(ipOrPubkey: string): boolean {
    return connecting.value.has(ipOrPubkey)
  }

  function clearError(): void {
    error.value = null
  }

  function reset(): void {
    peers.value = []
    savedPeers.value = []
    connecting.value.clear()
    error.value = null
  }

  return {
    peers,
    savedPeers,
    loading,
    connecting,
    error,
    onlinePeers,
    offlinePeers,
    connectingPeers,
    peerCount,
    onlinePeerCount,
    isConnecting,
    isPeerSaved,
    setRPC,
    loadPeers,
    connectToPeer,
    addSavedPeer,
    removeSavedPeer,
    savePeer,
    updateSavedPeerAlias,
    onForeground,
    handlePeerJoined,
    handlePeerLeft,
    handlePeerStatus,
    getPeerByPubkey,
    getPeerByIP,
    isPeerConnecting,
    clearError,
    reset,
  }
})
