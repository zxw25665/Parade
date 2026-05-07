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
    joinTeamWithName: JoinTeamWithName,
    leaveTeam: LeaveTeam,
    switchTeam: SwitchTeam,
    listTeams: ListTeams,
    getActiveTeam: GetActiveTeam,
    getPeers: GetPeers,
    sendTeamChat: SendTeamChat,
    sendPrivateChat: SendPrivateChat,
    sendChannelChat: SendChannelChat,
    createChannel: CreateChannel,
    listChannels: ListChannels,
    joinChannel: JoinChannel,
    leaveChannel: LeaveChannel,
    shareDirectory: ShareDirectory,
    unshareDirectory: UnshareDirectory,
    getDirectoryChildren: GetDirectoryChildren,
    getRemoteDirectoryChildren: GetRemoteDirectoryChildren,
    startDownload: StartDownload,
    getRecentHistory: GetRecentHistory,
    getRecentHistoryByChannel: GetRecentHistoryByChannel,
    connectToPeer,
    createShareGroup: CreateShareGroup,
    listShareGroups: ListShareGroups,
    addDirectoryToShareGroup: AddDirectoryToShareGroup,
    removeDirectoryFromShareGroup: RemoveDirectoryFromShareGroup,
    deleteShareGroup: DeleteShareGroup,
    getShareGroupDirs: GetShareGroupDirs
  }
}
