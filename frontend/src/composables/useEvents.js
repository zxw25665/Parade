import { reactive, onMounted, onUnmounted } from 'vue'
import { EventsOn, EventsOff } from '../lib/wailsjs/runtime/runtime'

const state = reactive({
  peers: [],
  teamMessages: [],
  privateMessages: [],
  downloads: {},
  completedDownloads: []
})

export function useEvents() {
  onMounted(() => {
    EventsOn('ui_peer_joined', (data) => {
      const p = { pubkey: data.PubKeyBase64 || data.pubkey, ip: data.IPAddress || data.ip }
      if (!state.peers.find(x => x.pubkey === p.pubkey)) {
        state.peers.push(p)
      }
    })

    EventsOn('ui_peer_left', (data) => {
      const pubkey = data.PubKeyBase64 || data.pubkey
      const idx = state.peers.findIndex(x => x.pubkey === pubkey)
      if (idx >= 0) state.peers.splice(idx, 1)
    })

    EventsOn('ui_new_message', (data) => {
      state.teamMessages.unshift({
        id: data.id,
        hlc: data.hlc,
        sender: data.sender,
        content: data.content,
        timestamp: data.timestamp,
        direction: 'receive'
      })
    })

    EventsOn('ui_private_message', (data) => {
      state.privateMessages.unshift({
        id: data.id,
        hlc: data.hlc,
        senderId: data.senderId,
        receiverId: data.receiverId,
        content: data.content,
        timestamp: data.timestamp,
        direction: data.senderId ? 'receive' : 'send'
      })
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
      const taskId = typeof data === 'string' ? data : (data.TaskID || data.taskId || data.task_id)
      if (state.downloads[taskId]) {
        state.completedDownloads.push({ ...state.downloads[taskId], completedAt: Date.now() })
        delete state.downloads[taskId]
      }
    })
  })

  onUnmounted(() => {
    EventsOff('ui_peer_joined', 'ui_peer_left', 'ui_new_message', 'ui_private_message', 'ui_file_progress', 'ui_file_completed')
  })

  return state
}
