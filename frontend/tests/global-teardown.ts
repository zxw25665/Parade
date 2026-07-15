import * as fs from 'fs'
import * as path from 'path'

const TEST_DATA_DIR = process.env.TEST_DATA_DIR || path.join(process.cwd(), 'test-data')

async function globalTeardown() {
  if (fs.existsSync(TEST_DATA_DIR)) {
    fs.rmSync(TEST_DATA_DIR, { recursive: true, force: true })
  }
  console.log('Global teardown complete')
}

export default globalTeardown
