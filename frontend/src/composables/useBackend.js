import {
  CheckHasIdentity,
  Register,
  Login,
  JoinTeam,
  JoinTeamWithName,
  LeaveTeam,
  SwitchTeam,
  ListTeams,
  GetActiveTeam,
  GetPeers,
  SendTeamChat,
  SendPrivateChat,
  SendChannelChat,
  CreateChannel,
  ListChannels,
  JoinChannel,
  LeaveChannel,
  ShareDirectory,
  UnshareDirectory,
  GetDirectoryChildren,
  GetRemoteDirectoryChildren,
  StartDownload,
  GetRecentHistory,
  GetRecentHistoryByChannel,
  CreateShareGroup,
  ListShareGroups,
  AddDirectoryToShareGroup,
  RemoveDirectoryFromShareGroup,
  DeleteShareGroup,
  GetShareGroupDirs
} from '../lib/wailsjs/go/app/App'
import { addLogEntry } from './useLogStore.js'

function logIPC(name, args, error, duration) {
  const msg = error
    ? `${name} ERROR (${duration}ms): ${error}`
    : `${name} ok (${duration}ms)`
  addLogEntry({
    time: new Date().toLocaleTimeString(),
    level: error ? 4 : 2,
    source: 'ipc',
    message: msg
  })
}

function logIPCCall(name, args) {
  addLogEntry({
    time: new Date().toLocaleTimeString(),
    level: 2,
    source: 'ipc',
    message: `${name} called`
  })
}

function wrapIPC(name, fn) {
  return async (...args) => {
    logIPCCall(name, args)
    const start = performance.now()
    try {
      const result = await fn(...args)
      const dur = (performance.now() - start).toFixed(1)
      logIPC(name, args, null, dur)
      return result
    } catch (e) {
      const dur = (performance.now() - start).toFixed(1)
      logIPC(name, args, String(e), dur)
      throw e
    }
  }
}

export function useBackend() {
  async function connectToPeer(ip) {
    const app = window.go?.app?.App
    if (app && app.ConnectToPeer) {
      return await app.ConnectToPeer(ip)
    }
    throw new Error('ConnectToPeer not available. Run wails dev to regenerate bindings.')
  }

  return {
    checkHasIdentity: wrapIPC('checkHasIdentity', CheckHasIdentity),
    register: wrapIPC('register', Register),
    login: wrapIPC('login', Login),
    joinTeam: wrapIPC('joinTeam', JoinTeam),
    joinTeamWithName: wrapIPC('joinTeamWithName', JoinTeamWithName),
    leaveTeam: wrapIPC('leaveTeam', LeaveTeam),
    switchTeam: wrapIPC('switchTeam', SwitchTeam),
    listTeams: wrapIPC('listTeams', ListTeams),
    getActiveTeam: wrapIPC('getActiveTeam', GetActiveTeam),
    getPeers: wrapIPC('getPeers', GetPeers),
    sendTeamChat: wrapIPC('sendTeamChat', SendTeamChat),
    sendPrivateChat: wrapIPC('sendPrivateChat', SendPrivateChat),
    sendChannelChat: wrapIPC('sendChannelChat', SendChannelChat),
    createChannel: wrapIPC('createChannel', CreateChannel),
    listChannels: wrapIPC('listChannels', ListChannels),
    joinChannel: wrapIPC('joinChannel', JoinChannel),
    leaveChannel: wrapIPC('leaveChannel', LeaveChannel),
    shareDirectory: wrapIPC('shareDirectory', ShareDirectory),
    unshareDirectory: wrapIPC('unshareDirectory', UnshareDirectory),
    getDirectoryChildren: wrapIPC('getDirectoryChildren', GetDirectoryChildren),
    getRemoteDirectoryChildren: wrapIPC('getRemoteDirectoryChildren', GetRemoteDirectoryChildren),
    startDownload: wrapIPC('startDownload', StartDownload),
    getRecentHistory: wrapIPC('getRecentHistory', GetRecentHistory),
    getRecentHistoryByChannel: wrapIPC('getRecentHistoryByChannel', GetRecentHistoryByChannel),
    connectToPeer: wrapIPC('connectToPeer', connectToPeer),
    createShareGroup: wrapIPC('createShareGroup', CreateShareGroup),
    listShareGroups: wrapIPC('listShareGroups', ListShareGroups),
    addDirectoryToShareGroup: wrapIPC('addDirectoryToShareGroup', AddDirectoryToShareGroup),
    removeDirectoryFromShareGroup: wrapIPC('removeDirectoryFromShareGroup', RemoveDirectoryFromShareGroup),
    deleteShareGroup: wrapIPC('deleteShareGroup', DeleteShareGroup),
    getShareGroupDirs: wrapIPC('getShareGroupDirs', GetShareGroupDirs)
  }
}
