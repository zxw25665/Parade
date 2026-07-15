import { computed } from 'vue'
import { useFilesStore } from '@/stores/files'
import type { FileEntry } from '@/lib/types'

export function useFiles() {
  const filesStore = useFilesStore()

  const localShared = computed(() => filesStore.localShared)
  const downloads = computed(() => filesStore.downloads)
  const shareGroups = computed(() => filesStore.shareGroups)
  const activeDownloads = computed(() => filesStore.activeDownloads)
  const completedDownloads = computed(() => filesStore.completedDownloads)
  const failedDownloads = computed(() => filesStore.failedDownloads)
  const downloadProgress = computed(() => filesStore.downloadProgress)
  const totalDownloaded = computed(() => filesStore.totalDownloaded)
  const loading = computed(() => filesStore.loading)
  const error = computed(() => filesStore.error)

  async function shareDirectory(path: string) {
    await filesStore.shareDirectory(path)
  }

  async function unshareDirectory(path: string) {
    await filesStore.unshareDirectory(path)
  }

  async function getLocalDirectoryChildren(path: string): Promise<FileEntry[]> {
    return await filesStore.getLocalDirectoryChildren(path)
  }

  async function getRemoteDirectoryChildren(targetUUID: string, path: string): Promise<FileEntry[]> {
    return await filesStore.getRemoteDirectoryChildren(targetUUID, path)
  }

  async function startDownload(targetUUID: string, virtualPath: string, localSavePath?: string) {
    await filesStore.startDownload(targetUUID, virtualPath, localSavePath)
  }

  async function getDefaultDownloadDir(): Promise<string> {
    return await filesStore.getDefaultDownloadDir()
  }

  async function loadShareGroups() {
    await filesStore.loadShareGroups()
  }

  async function createShareGroup(name: string): Promise<string> {
    return await filesStore.createShareGroup(name)
  }

  async function addDirectoryToShareGroup(groupId: string, dirPath: string) {
    await filesStore.addDirectoryToShareGroup(groupId, dirPath)
  }

  async function removeDirectoryFromShareGroup(groupId: string, dirPath: string) {
    await filesStore.removeDirectoryFromShareGroup(groupId, dirPath)
  }

  async function deleteShareGroup(groupId: string) {
    await filesStore.deleteShareGroup(groupId)
  }

  function handleFileProgress(payload: {
    task_id: string
    file_path: string
    transferred: number
    total_size: number
    is_upload: boolean
  }) {
    filesStore.handleFileProgress(payload)
  }

  function handleFileCompleted(taskId: string) {
    filesStore.handleFileCompleted(taskId)
  }

  function getCurrentPath(targetUUID: string): string {
    return filesStore.getCurrentPath(targetUUID)
  }

  function getRemoteEntries(targetUUID: string): FileEntry[] {
    return filesStore.getRemoteEntries(targetUUID)
  }

  function navigateToPath(targetUUID: string, path: string) {
    filesStore.navigateToPath(targetUUID, path)
  }

  function clearRemoteTree(targetUUID?: string) {
    filesStore.clearRemoteTree(targetUUID)
  }

  function removeDownload(taskId: string) {
    filesStore.removeDownload(taskId)
  }

  function clearCompletedDownloads() {
    filesStore.clearCompletedDownloads()
  }

  return {
    localShared,
    downloads,
    shareGroups,
    activeDownloads,
    completedDownloads,
    failedDownloads,
    downloadProgress,
    totalDownloaded,
    loading,
    error,
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
    clearError: filesStore.clearError,
    reset: filesStore.reset,
  }
}
