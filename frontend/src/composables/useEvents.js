import { reactive, onMounted, onUnmounted } from 'vue'
import { EventsOn, EventsOff } from '../lib/wailsjs/runtime/runtime'
import { addLogEntry } from './useLogStore.js'
import { useStore } from './useStore.js'

const state = reactive({
  peers: [],
  downloads: {},
  completedDownloads: []
})

function upsertPeerWithStatus(list, payload) {
  const pubkey = payload.PeerUUID || payload.uuid || payload.PubKeyBase64 || payload.pubkey
  if (!pubkey) return
  const idx = list.findIndex(p => p.pubkey === pubkey)
  const status = payload.status || (payload.Status)
  const next = {
    pubkey,
    ip: payload.IPAddress || payload.ip || (idx >= 0 ? list[idx].ip : ''),
    status: status || (idx >= 0 ? list[idx].status : 'offline'),
    last_heartbeat: payload.last_heartbeat || payload.LastHeartbeat || '',
    last_online: payload.last_online || payload.LastOnlineAt || ''
  }
  if (idx >= 0) list.splice(idx, 1, next)
  else list.push(next)
}

export function useEvents() {
  const store = useStore()

  onMounted(() => {
    EventsOn('ui_peer_joined', (data) => {
      const p = { pubkey: data.PeerUUID || data.uuid || data.PubKeyBase64 || data.pubkey, ip: data.IPAddress || data.ip }
      if (!state.peers.find(x => x.pubkey === p.pubkey)) {
        state.peers.push(p)
      }
      const alreadyOnline = store.peersWithStatus.find(x => x.pubkey === p.pubkey)
      if (!alreadyOnline) {
        store.peersWithStatus.push({
          pubkey: p.pubkey,
          ip: p.ip,
          status: 'online',
          last_heartbeat: '',
          last_online: ''
        })
      } else {
        upsertPeerWithStatus(store.peersWithStatus, { pubkey: p.pubkey, ip: p.ip, status: 'online' })
      }
    })

    EventsOn('ui_peer_left', (data) => {
      const pubkey = data.PeerUUID || data.uuid || data.PubKeyBase64 || data.pubkey
      const idx = state.peers.findIndex(x => x.pubkey === pubkey)
      if (idx >= 0) state.peers.splice(idx, 1)
      upsertPeerWithStatus(store.peersWithStatus, { pubkey, status: 'offline' })
    })

    EventsOn('ui_peer_status', (data) => {
      upsertPeerWithStatus(store.peersWithStatus, data)
    })

    EventsOn('ui_conversation_updated', () => {
      // Backend signals that conversations changed. Components watching
      // store.conversations should re-fetch via listConversations() — we
      // intentionally do not mutate here so the data source stays canonical.
    })

    EventsOn('ui_new_message', (data) => {
      const convId = data.conversation_id || data.ConversationID || data.conversationId || ''
      if (!convId) return
      if (!store.messagesByConv[convId]) {
        store.messagesByConv[convId] = []
      }
      const arr = store.messagesByConv[convId]
      if (arr.find(m => m.id === data.id)) return
      arr.push({
        id: data.id,
        hlc: data.hlc,
        sender: data.sender,
        content: data.content,
        timestamp: data.timestamp,
        conversationId: convId,
        direction: data.sender === store.pubkey ? 'send' : 'receive'
      })
      arr.sort((a, b) => (a.hlc || '').localeCompare(b.hlc || ''))
    })

    EventsOn('ui_file_progress', (data) => {
      state.downloads[data.TaskID || data.taskId || data.task_id] = {
        taskId: data.TaskID || data.taskId || data.task_id,
        filePath: data.FilePath || data.filePath || data.file_path,
        transferred: data.Transferred != null ? data.Transferred : data.transferred,
        totalSize: data.TotalSize != null ? data.TotalSize : data.totalSize,
        isUpload: data.IsUpload != null ? data.IsUpload : data.isUpload
      }
    })

    EventsOn('ui_file_completed', (data) => {
      const taskId = typeof data === 'string' ? data
        : (data.TaskID || data.taskId || data.task_id || data.data || '')
      if (taskId && state.downloads[taskId]) {
        state.completedDownloads.push({ ...state.downloads[taskId], completedAt: Date.now() })
        delete state.downloads[taskId]
      }
    })

    EventsOn('ui_log', (data) => {
      addLogEntry(data)
    })
  })

  onUnmounted(() => {
    EventsOff('ui_peer_joined', 'ui_peer_left', 'ui_peer_status', 'ui_conversation_updated',
              'ui_new_message', 'ui_file_progress', 'ui_file_completed', 'ui_log')
  })

  return state
}
