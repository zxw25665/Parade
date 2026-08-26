import { defineConfig, devices } from '@playwright/test'
import path from 'path'

// Base URL for the app - can be overridden via CLI
const baseURL = process.env.PLAYWRIGHT_BASE_URL || 'http://localhost:5173'

// CDP endpoint for connecting to an existing browser instance
// Set PLAYWRIGHT_CDP_ENDPOINT=ws://localhost:9222/devtools/browser/<id>
// or PLAYWRIGHT_CDP_ENDPOINT=http://localhost:9222
const cdpEndpoint = process.env.PLAYWRIGHT_CDP_ENDPOINT

export default defineConfig({
  testDir: './tests',
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: 1,
  reporter: [
    ['list'],
    ['html', { open: 'never' }],
    ['json', { outputFile: 'test-results/results.json' }],
  ],
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    actionTimeout: 10000,
    navigationTimeout: 30000,
  },
  globalSetup: './tests/global-setup.ts',
  globalTeardown: './tests/global-teardown.ts',
  projects: [
    // Standard project: Playwright launches its own Chromium
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        channel: undefined,
        launchOptions: {
          args: [
            '--disable-dev-shm-usage',
            '--no-sandbox',
          ],
        },
      },
    },
    // CDP project: connects to an existing Chrome with remote debugging enabled
    // Only included when PLAYWRIGHT_CDP_ENDPOINT is set
    ...(cdpEndpoint ? [{
      name: 'chromium-cdp',
      use: {
        ...devices['Desktop Chrome'],
        connectOptions: {
          wsEndpoint: cdpEndpoint.startsWith('ws://')
            ? cdpEndpoint
            : `ws://${cdpEndpoint.replace(/^https?:\/\//, '')}`,
        },
      },
    }] : []),
  ],
  webServer: process.env.SKIP_WEB_SERVER
    ? undefined
    : {
        command: 'pnpm run dev',
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 120000,
        stdout: 'ignore',
        stderr: 'pipe',
      },
})
