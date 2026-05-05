import {
  CheckHasIdentity,
  Register,
  Login,
  JoinTeam,
  GetPeers,
  SendTeamChat,
  SendPrivateChat,
  ShareDirectory,
  UnshareDirectory,
  GetDirectoryChildren,
  StartDownload,
  GetRecentHistory
} from '../lib/wailsjs/go/app/App'

export function useBackend() {
  async function connectToPeer(ip) {
    const app = window.go?.app?.App
    if (app && app.ConnectToPeer) {
      return await app.ConnectToPeer(ip)
    }
    throw new Error('ConnectToPeer not available. Run wails dev to regenerate bindings.')
  }

  return {
    checkHasIdentity: CheckHasIdentity,
    register: Register,
    login: Login,
    joinTeam: JoinTeam,
    getPeers: GetPeers,
    sendTeamChat: SendTeamChat,
    sendPrivateChat: SendPrivateChat,
    shareDirectory: ShareDirectory,
    unshareDirectory: UnshareDirectory,
    getDirectoryChildren: GetDirectoryChildren,
    startDownload: StartDownload,
    getRecentHistory: GetRecentHistory,
    connectToPeer
  }
}
