import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { ParadeRPC } from '@/lib/rpc-client'
import type { FileEntry } from '@/lib/types'

export interface DownloadTask {
  id: string
  taskId: string
  fileName: string
  filePath: string
  targetPeer: string
  transferred: number
  totalSize: number
  status: 'pending' | 'downloading' | 'completed' | 'failed' | 'paused'
  error?: string
  startedAt: string
  completedAt?: string
}

export interface ShareGroup {
  id: string
  teamId: string
  name: string
  createdBy: string
  createdAt: string
  directories: string[]
}

export const useFilesStore = defineStore('files', () => {
  const localShared = ref<string[]>([])
  const downloads = ref<DownloadTask[]>([])
  const remoteTree = ref<Record<string, FileEntry[]>>({})
  const shareGroups = ref<ShareGroup[]>([])
  const currentPaths = ref<Record<string, string>>({})
  const loading = ref(false)
  const error = ref<string | null>(null)

  let rpc: ParadeRPC | null = null

  const activeDownloads = computed(() =>
    downloads.value.filter(d => d.status === 'downloading' || d.status === 'pending')
  )
  const completedDownloads = computed(() =>
    downloads.value.filter(d => d.status === 'completed')
  )
  const failedDownloads = computed(() =>
    downloads.value.filter(d => d.status === 'failed')
  )
  const downloadProgress = computed(() => {
    const active = activeDownloads.value
    if (active.length === 0) return 0
    const total = active.reduce((sum, d) => sum + d.totalSize, 0)
    const transferred = active.reduce((sum, d) => sum + d.transferred, 0)
    return total > 0 ? (transferred / total) * 100 : 0
  })
  const totalDownloaded = computed(() =>
    completedDownloads.value.reduce((sum, d) => sum + d.totalSize, 0)
  )

  function setRPC(rpcInstance: ParadeRPC) {
    rpc = rpcInstance
  }

  async function shareDirectory(path: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      await rpc.shareDirectory(path)
      if (!localShared.value.includes(path)) {
        localShared.value.push(path)
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to share directory'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function unshareDirectory(path: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      await rpc.unshareDirectory(path)
      localShared.value = localShared.value.filter(p => p !== path)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to unshare directory'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function getLocalDirectoryChildren(path: string): Promise<FileEntry[]> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      return await rpc.getDirectoryChildren(path)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to get directory contents'
      throw e
    }
  }

  async function getRemoteDirectoryChildren(targetUUID: string, path: string): Promise<FileEntry[]> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      const entries = await rpc.getRemoteDirectoryChildren(targetUUID, path)
      remoteTree.value[targetUUID] = entries
      currentPaths.value[targetUUID] = path
      return entries
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to get remote directory contents'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function startDownload(
    targetUUID: string,
    virtualPath: string,
    localSavePath?: string
  ): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    const savePath = localSavePath ?? virtualPath.split('/').pop() ?? 'download'
    const taskId = `${targetUUID}-${virtualPath}-${Date.now()}`
    downloads.value.push({
      id: taskId,
      taskId,
      fileName: virtualPath.split('/').pop() ?? 'unknown',
      filePath: virtualPath,
      targetPeer: targetUUID,
      transferred: 0,
      totalSize: 0,
      status: 'pending',
      startedAt: new Date().toISOString(),
    })
    try {
      await rpc.startDownload(targetUUID, virtualPath, savePath)
    } catch (e) {
      const task = downloads.value.find(d => d.id === taskId)
      if (task) {
        task.status = 'failed'
        task.error = e instanceof Error ? e.message : 'Download failed'
      }
      error.value = e instanceof Error ? e.message : 'Failed to start download'
      throw e
    }
  }

  async function getDefaultDownloadDir(): Promise<string> {
    if (!rpc) throw new Error('RPC not initialized')
    try {
      return await rpc.getDefaultDownloadDir()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to get default download directory'
      throw e
    }
  }

  async function loadShareGroups(): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      const groups = await rpc.listShareGroups()
      shareGroups.value = groups.map(g => ({
        id: g.id,
        teamId: g.team_id,
        name: g.name,
        createdBy: g.created_by,
        createdAt: g.created_at,
        directories: [],
      }))
      await Promise.all(
        shareGroups.value.map(async (group) => {
          const dirs = await rpc!.getShareGroupDirs(group.id)
          group.directories = dirs.map(d => d.dir_path)
        })
      )
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load share groups'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function createShareGroup(name: string): Promise<string> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      const groupId = await rpc.createShareGroup(name)
      shareGroups.value.push({
        id: groupId,
        teamId: '',
        name,
        createdBy: '',
        createdAt: new Date().toISOString(),
        directories: [],
      })
      return groupId
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to create share group'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function addDirectoryToShareGroup(groupId: string, dirPath: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      await rpc.addDirectoryToShareGroup(groupId, dirPath)
      const group = shareGroups.value.find(g => g.id === groupId)
      if (group && !group.directories.includes(dirPath)) {
        group.directories.push(dirPath)
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to add directory to share group'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function removeDirectoryFromShareGroup(groupId: string, dirPath: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      await rpc.removeDirectoryFromShareGroup(groupId, dirPath)
      const group = shareGroups.value.find(g => g.id === groupId)
      if (group) {
        group.directories = group.directories.filter(d => d !== dirPath)
      }
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to remove directory from share group'
      throw e
    } finally {
      loading.value = false
    }
  }

  async function deleteShareGroup(groupId: string): Promise<void> {
    if (!rpc) throw new Error('RPC not initialized')
    loading.value = true
    error.value = null
    try {
      await rpc.deleteShareGroup(groupId)
      shareGroups.value = shareGroups.value.filter(g => g.id !== groupId)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to delete share group'
      throw e
    } finally {
      loading.value = false
    }
  }

  function handleFileProgress(payload: {
    task_id: string
    file_path: string
    transferred: number
    total_size: number
    is_upload: boolean
  }): void {
    if (payload.is_upload) return
    const task = downloads.value.find(d => d.taskId === payload.task_id)
    if (task) {
      task.status = 'downloading'
      task.transferred = payload.transferred
      task.totalSize = payload.total_size
    }
  }

  function handleFileCompleted(taskId: string): void {
    const task = downloads.value.find(d => d.taskId === taskId)
    if (task) {
      task.status = 'completed'
      task.completedAt = new Date().toISOString()
    }
  }

  function getCurrentPath(targetUUID: string): string {
    return currentPaths.value[targetUUID] ?? '/'
  }

  function getRemoteEntries(targetUUID: string): FileEntry[] {
    return remoteTree.value[targetUUID] ?? []
  }

  function navigateToPath(targetUUID: string, path: string): void {
    currentPaths.value[targetUUID] = path
  }

  function clearRemoteTree(targetUUID?: string): void {
    if (targetUUID) {
      delete remoteTree.value[targetUUID]
      delete currentPaths.value[targetUUID]
    } else {
      remoteTree.value = {}
      currentPaths.value = {}
    }
  }

  function removeDownload(taskId: string): void {
    downloads.value = downloads.value.filter(d => d.taskId !== taskId)
  }

  function clearCompletedDownloads(): void {
    downloads.value = downloads.value.filter(d => d.status !== 'completed')
  }

  function clearError(): void {
    error.value = null
  }

  function reset(): void {
    localShared.value = []
    downloads.value = []
    remoteTree.value = {}
    shareGroups.value = []
    currentPaths.value = {}
    error.value = null
  }

  return {
    localShared,
    downloads,
    remoteTree,
    shareGroups,
    loading,
    error,
    activeDownloads,
    completedDownloads,
    failedDownloads,
    downloadProgress,
    totalDownloaded,
    setRPC,
    shareDirectory,
    unshareDirectory,
    getLocalDirectoryChildren,
    getRemoteDirectoryChildren,
    startDownload,
    getDefaultDownloadDir,
    loadShareGroups,
    createShareGroup,
    addDirectoryToShareGroup,
    removeDirectoryFromShareGroup,
    deleteShareGroup,
    handleFileProgress,
    handleFileCompleted,
    getCurrentPath,
    getRemoteEntries,
    navigateToPath,
    clearRemoteTree,
    removeDownload,
    clearCompletedDownloads,
    clearError,
    reset,
  }
})
