import * as fs from 'fs'
import * as path from 'path'

const TEST_DATA_DIR = process.env.TEST_DATA_DIR || path.join(process.cwd(), 'test-data')

async function globalTeardown() {
  // Stop daemon proxy first (before removing data dir)
  console.log('[global-teardown] Stopping daemon proxy...')
  try {
    const { stopDaemonProxy } = await import('./daemon-proxy')
    await stopDaemonProxy()
    console.log('[global-teardown] Daemon proxy stopped')
  } catch (err) {
    console.error('[global-teardown] Failed to stop daemon proxy:', err)
  }

  // Clean up test data directory
  if (fs.existsSync(TEST_DATA_DIR)) {
    fs.rmSync(TEST_DATA_DIR, { recursive: true, force: true })
    console.log('[global-teardown] Test data directory removed')
  }

  console.log('Global teardown complete')
}

export default globalTeardown
