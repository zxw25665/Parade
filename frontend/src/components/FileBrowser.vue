<template>

    <!-- Mode toggle -->
    <div class="row" style="margin-bottom:16px">
      <button
        @click="switchMode('local')"
        :class="mode === 'local' ? 'badge badge-green' : 'badge'"
        style="margin-right:8px;cursor:pointer"
      >
        {{ $t('file.local') }}
      </button>
      <button
        @click="switchMode('remote')"
        :class="mode === 'remote' ? 'badge badge-green' : 'badge'"
        style="cursor:pointer"
      >
        {{ $t('file.remote') }}
      </button>
    </div>

    <!-- LOCAL mode -->
    <div v-if="mode === 'local'">
      <!-- Share directory -->
      <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">{{ $t('file.shareDirectory') }}</h3>
      <div class="row">
        <input v-model="sharePath" :placeholder="$t('file.sharePath')" style="flex:1" />
        <button @click="doShare" :disabled="!sharePath || loading">{{ $t('file.share') }}</button>
      </div>

      <!-- Shared directories list -->
      <div v-if="sharedDirs.length" class="list" style="margin-bottom:12px">
        <div class="list-item" v-for="d in sharedDirs" :key="d">
          <span style="flex:1;font-size:12px">{{ d }}</span>
          <button @click="doUnshare(d)" style="font-size:11px;padding:2px 8px">{{ $t('file.unshare') }}</button>
        </div>
      </div>

      <!-- Local directory browser -->
      <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">{{ $t('file.browseLocal') }}</h3>
      <div v-if="browsePath" style="font-size:12px;margin-bottom:8px;color:#8a8aaf">
        {{ $t('file.path') }}: {{ browsePath }}
        <button @click="doBrowseUp" style="font-size:11px;padding:2px 8px;margin-left:8px">{{ $t('file.up') }}</button>
      </div>
      <div class="list" v-if="dirEntries.length">
        <div class="list-item" v-for="entry in dirEntries" :key="entry.path">
          <span style="cursor:pointer" @click="doClickEntry(entry)">
            {{ entry.isFolder ? '📁' : '📄' }}
            {{ entry.name }}
            <span v-if="!entry.isFolder" style="font-size:11px;color:#8a8aaf">
              ({{ formatSize(entry.size) }})
            </span>
          </span>
        </div>
      </div>
      <div v-else-if="!loading" style="font-size:13px;color:#8a8aaf;margin-top:8px">
        {{ $t('file.clickToBrowse') }}
      </div>
    </div>

    <!-- REMOTE mode -->
    <div v-if="mode === 'remote'">
      <!-- Peer selector -->
      <h3 style="font-size:14px;margin:12px 0 6px;color:#aaa">{{ $t('file.selectPeer') }}</h3>
      <div class="row">
        <select v-model="browsePeer" style="width:100%">
          <option value="">{{ $t('chat.selectPeer') }}</option>
          <option v-for="p in peers" :key="p.pubkey" :value="p.pubkey">
            {{ p.ip }} ({{ p.pubkey.slice(0, 12) }}...)
          </option>
        </select>
      </div>
      <div class="row">
        <button @click="doBrowse" :disabled="!browsePeer || loading">{{ $t('file.browse') }}</button>
      </div>

      <!-- Path breadcrumb -->
      <div v-if="browsePath !== ''" style="font-size:12px;margin:12px 0 8px;color:#8a8aaf">
        {{ $t('file.path') }}: {{ browsePath || '/' }}
        <button @click="doBrowseUp" style="font-size:11px;padding:2px 8px;margin-left:8px">{{ $t('file.up') }}</button>
        <button @click="doBrowseRoot" style="font-size:11px;padding:2px 8px;margin-left:4px">{{ $t('file.root') }}</button>
      </div>

      <!-- Remote directory listing -->
      <div class="list" v-if="dirEntries.length" style="margin-top:12px">
        <div class="list-item" v-for="entry in dirEntries" :key="entry.path">
          <span style="cursor:pointer;flex:1" @click="doClickEntry(entry)">
            {{ entry.isFolder ? '📁' : '📄' }}
            {{ entry.name }}
            <span v-if="!entry.isFolder" style="font-size:11px;color:#8a8aaf">
              ({{ formatSize(entry.size) }})
            </span>
          </span>
          <button
            v-if="!entry.isFolder"
            @click="doDownload(entry)"
            style="font-size:11px;padding:2px 8px"
          >
            {{ $t('file.download') }}
          </button>
        </div>
      </div>
      <div v-else-if="browsePeer && !loading" style="font-size:13px;color:#8a8aaf;margin-top:8px">
        {{ $t('file.emptyDir') }}
      </div>

      <!-- Download form -->
      <div v-if="downloadTarget" style="margin-top:16px;padding:12px;background:#0f3460;border-radius:6px">
        <h3 style="font-size:14px;margin:0 0 8px 0;color:#aaa">{{ $t('file.downloading') }}: {{ downloadTarget.name }}</h3>
        <div class="row">
          <input v-model="localSavePath" :placeholder="$t('file.localSavePath')" style="flex:1" />
          <button @click="doStartDownload" :disabled="!localSavePath || loading">{{ $t('file.startDownload') }}</button>
        </div>
      </div>
    </div>

    <div v-if="errorMsg" class="error">{{ errorMsg }}</div>
    <div v-if="successMsg" class="success">{{ successMsg }}</div>
</template>

<script setup>
import { ref, computed, inject, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBackend } from '../composables/useBackend.js'

const { t } = useI18n()

const {
  shareDirectory,
  unshareDirectory,
  getDirectoryChildren,
  getRemoteDirectoryChildren,
  startDownload,
  getDefaultDownloadDir
} = useBackend()

const state = inject('events')
const store = inject('store')
const peers = computed(() => state.peers)

const mode = ref('local')
const browsePeer = ref('')
const browsePath = ref('')
const dirEntries = ref([])

function switchMode(m) {
  mode.value = m
  browsePath.value = ''
  dirEntries.value = []
}
const sharedDirs = ref([])
const sharePath = ref('')
const downloadTarget = ref(null)
const localSavePath = ref('')
const downloadDir = ref('/tmp')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

onMounted(async () => {
  try {
    downloadDir.value = await getDefaultDownloadDir()
  } catch {
    // use /tmp fallback
  }
})

function normalizeEntry(entry) {
  return {
    name: entry.Name || entry.name,
    path: entry.Path || entry.path,
    isFolder: entry.IsFolder || entry.isFolder || entry.isDirectory || entry.is_directory,
    size: entry.Size || entry.size
  }
}

async function doShare() {
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    await shareDirectory(sharePath.value)
    sharedDirs.value.push(sharePath.value)
    successMsg.value = t('file.shared') + `: ${sharePath.value}`
    sharePath.value = ''
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doUnshare(dir) {
  loading.value = true
  errorMsg.value = ''
  try {
    await unshareDirectory(dir)
    sharedDirs.value = sharedDirs.value.filter(d => d !== dir)
    successMsg.value = t('file.unshared') + `: ${dir}`
  } catch (e) {
    errorMsg.value = e.toString()
  } finally {
    loading.value = false
  }
}

async function doBrowse() {
  loading.value = true
  errorMsg.value = ''
  browsePath.value = ''
  dirEntries.value = []
  try {
    let entries
    if (mode.value === 'local') {
      entries = await getDirectoryChildren('')
    } else {
      entries = await getRemoteDirectoryChildren(browsePeer.value, '')
    }
    dirEntries.value = Array.isArray(entries) ? entries.map(normalizeEntry) : []
  } catch (e) {
    errorMsg.value = e.toString()
    dirEntries.value = []
  } finally {
    loading.value = false
  }
}

async function doClickEntry(entry) {
  if (entry.isFolder) {
    loading.value = true
    errorMsg.value = ''
    browsePath.value = entry.path
    try {
      let entries
      if (mode.value === 'local') {
        entries = await getDirectoryChildren(entry.path)
      } else {
        entries = await getRemoteDirectoryChildren(browsePeer.value, entry.path)
      }
      dirEntries.value = Array.isArray(entries) ? entries.map(normalizeEntry) : []
    } catch (e) {
      errorMsg.value = e.toString()
      dirEntries.value = []
    } finally {
      loading.value = false
    }
  }
}

async function doBrowseRoot() {
  browsePath.value = ''
  await doBrowse()
}

async function doBrowseUp() {
  if (!browsePath.value) return
  const parts = browsePath.value.replace(/\\/g, '/').split('/')
  parts.pop()
  const parentPath = parts.join('/')
  browsePath.value = parentPath
  loading.value = true
  errorMsg.value = ''
  try {
    let entries
    if (mode.value === 'local') {
      entries = await getDirectoryChildren(parentPath)
    } else {
      entries = await getRemoteDirectoryChildren(browsePeer.value, parentPath)
    }
    dirEntries.value = Array.isArray(entries) ? entries.map(normalizeEntry) : []
  } catch (e) {
    errorMsg.value = e.toString()
    dirEntries.value = []
  } finally {
    loading.value = false
  }
}

function doDownload(entry) {
  downloadTarget.value = entry
  localSavePath.value = downloadDir.value + '/' + entry.name
}

async function doStartDownload() {
  if (!downloadTarget.value || !browsePeer.value || !localSavePath.value) {
    errorMsg.value = 'DEBUG: blocked - dT=' + !!downloadTarget.value + ' peer=' + !!browsePeer.value + ' path=' + !!localSavePath.value
    return
  }
  loading.value = true
  errorMsg.value = ''
  successMsg.value = 'DEBUG: calling startDownload(' + browsePeer.value.slice(0,8) + '..., ' + downloadTarget.value.path + ', ' + localSavePath.value + ')'
  const vpath = downloadTarget.value.path
  try {
    await startDownload(browsePeer.value, vpath, localSavePath.value)
    successMsg.value = t('file.downloadStarted') + `: ${downloadTarget.value.name}`
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
  while (s >= 1024 && i < u.length - 1) {
    s /= 1024
    i++
  }
  return s.toFixed(1) + ' ' + u[i]
}
</script>
