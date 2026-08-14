import type { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

const TEST_DATA_DIR = process.env.TEST_DATA_DIR || path.join(process.cwd(), 'test-data')
const PROXY_PORT = parseInt(process.env.DAEMON_PROXY_PORT || '9876', 10)

async function globalSetup(_config: FullConfig) {
  // Prepare test data directory
  if (fs.existsSync(TEST_DATA_DIR)) {
    fs.rmSync(TEST_DATA_DIR, { recursive: true, force: true })
  }
  fs.mkdirSync(TEST_DATA_DIR, { recursive: true })

  process.env.TEST_DATA_DIR = TEST_DATA_DIR
  process.env.DAEMON_PROXY_PORT = String(PROXY_PORT)

  // Start daemon proxy
  console.log('[global-setup] Starting daemon proxy...')
  try {
    const { startDaemonProxy } = await import('./daemon-proxy')
    const daemonDataDir = path.join(TEST_DATA_DIR, 'daemon')
    const result = await startDaemonProxy(daemonDataDir, PROXY_PORT)
    process.env.DAEMON_PROXY_URL = result.url
    process.env.DAEMON_PROXY_PORT = String(result.port)
    console.log(`[global-setup] Daemon proxy ready: ${result.url}`)
    delete process.env.DAEMON_PROXY_SKIP
  } catch (err) {
    console.error('[global-setup] Failed to start daemon proxy:', err)
    process.env.DAEMON_PROXY_SKIP = 'true'
    // Don't fail — let tests conditionally skip if daemon is unavailable
    console.log('[global-setup] Tests requiring daemon will be skipped')
  }

  // Ping Vite dev server
  try {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 1000)
    await fetch('http://localhost:5173', {
      method: 'HEAD',
      signal: controller.signal,
    }).catch(() => null)
    clearTimeout(timeout)
  } catch {
    // Server not running yet — webServer in playwright.config.ts will start it
  }

  console.log(`Test data directory: ${TEST_DATA_DIR}`)
}

export default globalSetup
