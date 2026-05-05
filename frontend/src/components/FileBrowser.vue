<template>
  <div class="panel">
    <h2>File Sharing &amp; Browsing</h2>

    <!-- Share -->
    <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">Share a Directory</h3>
    <div class="row">
      <input v-model="sharePath" placeholder="/absolute/path/to/dir" style="flex:1" />
      <button @click="doShare" :disabled="!sharePath || loading">Share</button>
    </div>
    <div v-if="sharedDirs.length" class="list" style="margin-bottom:12px">
      <div class="list-item" v-for="d in sharedDirs" :key="d">
        <span style="flex:1;font-size:12px">{{ d }}</span>
        <button @click="doUnshare(d)" style="font-size:11px;padding:2px 8px">Unshare</button>
      </div>
    </div>

    <!-- Browse peer -->
    <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">Browse Peer Files</h3>
    <div class="row">
      <select v-model="browsePeer" style="flex:1">
        <option value="">-- Select peer --</option>
        <option v-for="p in peers" :key="p.pubkey" :value="p.pubkey">
          {{ p.ip }} ({{ p.pubkey.slice(0, 12) }}...)
        </option>
      </select>
      <button @click="doBrowse" :disabled="!browsePeer || loading">Browse</button>
    </div>
    <div v-if="browsePath" style="font-size:12px;margin-bottom:8px;color:#8a8aaf">
      Path: {{ browsePath }}
      <button @click="doBrowseUp" style="font-size:11px;padding:2px 8px" :disabled="browsePath === ''">Up</button>
    </div>
    <div class="list" v-if="dirEntries.length">
      <div class="list-item" v-for="entry in dirEntries" :key="entry.Path || entry.path">
        <span style="cursor:pointer" @click="doClickEntry(entry)">
          {{ (entry.IsFolder || entry.isFolder) ? '📁' : '📄' }}
          {{ entry.Name || entry.name }}
          <span v-if="!(entry.IsFolder || entry.isFolder)" style="font-size:11px;color:#8a8aaf">
            ({{ formatSize(entry.Size || entry.size) }})
          </span>
        </span>
        <span v-if="!(entry.IsFolder || entry.isFolder)">
          <button @click="doDownload(entry)" style="font-size:11px;padding:2px 8px">Download</button>
        </span>
      </div>
    </div>
    <div v-else-if="browsePeer && dirEntries.length === 0 && !loading" style="font-size:13px;color:#8a8aaf;margin-top:8px">
      Empty directory or unable to browse
    </div>

    <!-- Download -->
    <div v-if="downloadTarget" style="margin-top:12px">
      <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">Download: {{ downloadTarget.name || downloadTarget.Name }}</h3>
      <div class="row">
        <input v-model="localSavePath" placeholder="Local save path" style="flex:1" />
        <button @click="doStartDownload" :disabled="!localSavePath || loading">Start Download</button>
      </div>
    </div>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
  </div>
</template>

<script setup>
import { ref, computed, inject } from 'vue'
import { useBackend } from '../composables/useBackend.js'

const { shareDirectory, unshareDirectory, getDirectoryChildren, startDownload } = useBackend()
const state = inject('events')
const peers = computed(() => state.peers)

const sharePath = ref('')
const sharedDirs = ref([])
const browsePeer = ref('')
const browsePath = ref('')
const dirEntries = ref([])
const downloadTarget = ref(null)
const localSavePath = ref('')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

async function doShare() {
  loading.value = true; errorMsg.value = ''; successMsg.value = ''
  try {
    await shareDirectory(sharePath.value)
    sharedDirs.value.push(sharePath.value)
    successMsg.value = `Shared: ${sharePath.value}`
    sharePath.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doUnshare(dir) {
  loading.value = true; errorMsg.value = ''
  try {
    await unshareDirectory(dir)
    sharedDirs.value = sharedDirs.value.filter(d => d !== dir)
    successMsg.value = `Unshared: ${dir}`
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doBrowse() {
  loading.value = true; errorMsg.value = ''
  browsePath.value = ''
  try {
    const entries = await getDirectoryChildren('')
    dirEntries.value = Array.isArray(entries) ? entries : []
    if (dirEntries.value.length === 0) {
      errorMsg.value = 'No files or directory is empty'
    }
  } catch (e) {
    errorMsg.value = e.toString()
    dirEntries.value = []
  } finally {
    loading.value = false
  }
}

async function doClickEntry(entry) {
  const isFolder = entry.IsFolder || entry.isFolder
  if (isFolder) {
    loading.value = true; errorMsg.value = ''
    const name = entry.Name || entry.name
    const path = entry.Path || entry.path || name
    browsePath.value = browsePath.value ? browsePath.value + '/' + name : name
    try {
      const entries = await getDirectoryChildren(path)
      dirEntries.value = Array.isArray(entries) ? entries : []
    } catch (e) {
      errorMsg.value = e.toString()
      dirEntries.value = []
    } finally {
      loading.value = false
    }
  }
}

async function doBrowseUp() {
  if (!browsePath.value) return
  const parts = browsePath.value.split('/')
  parts.pop()
  browsePath.value = parts.join('/')
  loading.value = true; errorMsg.value = ''
  try {
    const entries = await getDirectoryChildren(browsePath.value || '')
    dirEntries.value = Array.isArray(entries) ? entries : []
  } catch (e) {
    errorMsg.value = e.toString()
    dirEntries.value = []
  } finally {
    loading.value = false
  }
}

function doDownload(entry) {
  downloadTarget.value = entry
  const name = entry.Name || entry.name
  localSavePath.value = '/tmp/' + name
}

async function doStartDownload() {
  if (!downloadTarget.value || !browsePeer.value || !localSavePath.value) return
  loading.value = true; errorMsg.value = ''; successMsg.value = ''
  const name = downloadTarget.value.Name || downloadTarget.value.name
  const vpath = browsePath.value ? browsePath.value + '/' + name : name
  try {
    await startDownload(browsePeer.value, vpath, localSavePath.value)
    successMsg.value = `Download started: ${name}`
    downloadTarget.value = null
    localSavePath.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

function formatSize(bytes) {
  if (!bytes) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let s = bytes
  while (s >= 1024 && i < u.length - 1) { s /= 1024; i++ }
  return s.toFixed(1) + ' ' + u[i]
}
</script>
