import type { FullConfig } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

const TEST_DATA_DIR = process.env.TEST_DATA_DIR || path.join(process.cwd(), 'test-data')

async function globalSetup(_config: FullConfig) {
  if (fs.existsSync(TEST_DATA_DIR)) {
    fs.rmSync(TEST_DATA_DIR, { recursive: true, force: true })
  }
  fs.mkdirSync(TEST_DATA_DIR, { recursive: true })
  
  process.env.TEST_DATA_DIR = TEST_DATA_DIR
  
  try {
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), 1000)
    await fetch('http://localhost:5173', { 
      method: 'HEAD',
      signal: controller.signal 
    }).catch(() => null)
    clearTimeout(timeout)
  } catch {
    // Server not running yet
  }
  
  console.log(`Test data directory: ${TEST_DATA_DIR}`)
}

export default globalSetup
