import { test as base, expect, Page } from '@playwright/test'
import { fileURLToPath } from 'url'
import path from 'path'

const __filename = fileURLToPath(import.meta.url)
const __dirname = path.dirname(__filename)

const TEST_PASSWORD = 'Test@123456'
const STRONG_PASSWORD = 'MyStr0ng!Pass#2024'

// ── Custom fixture that injects Tauri API mock before each test ──────────

export const test = base.extend<{ autoInjectMock: void }>({
  page: async ({ page }, use) => {
    const port = process.env.DAEMON_PROXY_PORT || '9876'

    // Inject Tauri API mock BEFORE the page loads
    await page.addInitScript({ path: path.resolve(__dirname, 'tauri-mock.js') })
    await page.addInitScript(`window.__PARADE_DAEMON_PROXY_PORT = '${port}'`)

    await use(page)
  },
})

// ── Helper utilities ─────────────────────────────────────────────────────

export async function clearBrowserData(page: Page) {
  await page.context().clearCookies()
  await page.evaluate(() => localStorage.clear())
}

export async function waitForPageReady(page: Page, timeout = 10000) {
  await page.waitForLoadState('networkidle', { timeout })
}

export async function waitForElement(page: Page, selector: string, timeout = 5000) {
  await page.waitForSelector(selector, { timeout, state: 'visible' })
}

export async function fillPassword(page: Page, password: string) {
  const input = page.locator('input[type="password"]').first()
  await input.fill(password)
}

export async function clickButton(page: Page, text: string) {
  await page.locator('button', { hasText: text }).click()
}

export async function getSecretFromInput(page: Page): Promise<string> {
  return await page.locator('#create-secret').inputValue()
}

export async function generateSecret(page: Page) {
  await page.locator('button', { hasText: 'Generate' }).click()
  return await getSecretFromInput(page)
}

/**
 * Wait for the RPC mock to be connected to the daemon proxy.
 * Uses window.__PARADE_RPC_READY flag set by tauri-mock.js.
 */
export async function waitForRPCReady(page: Page, timeout = 15_000): Promise<void> {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    const ready = await page.evaluate(() => (window as any).__PARADE_RPC_READY)
    if (ready) return
    await page.waitForTimeout(200)
  }
  throw new Error('RPC connection not ready after ' + timeout + 'ms')
}

/**
 * Register a new identity via the mock RPC bridge.
 */
export async function registerIdentity(page: Page, password: string = TEST_PASSWORD): Promise<void> {
  await page.evaluate(async (pwd) => {
    const internals = (window as any).__TAURI_INTERNALS__
    if (!internals) throw new Error('__TAURI_INTERNALS__ not found')
    await internals.invoke('register', { password: pwd })
  }, password)
}

/**
 * Login via the mock RPC bridge.
 */
export async function loginViaRPC(page: Page, password: string = TEST_PASSWORD): Promise<void> {
  await page.evaluate(async (pwd) => {
    const internals = (window as any).__TAURI_INTERNALS__
    if (!internals) throw new Error('__TAURI_INTERNALS__ not found')
    await internals.invoke('login', { password: pwd })
  }, password)
}

export { expect }

/**
 * Set up authenticated state via the Pinia auth store.
 * Must be called AFTER navigating to the app (the store needs to be initialized).
 * Registers a new identity and logs in, setting isLoggedIn + hasIdentity to true.
 */
export async function setupAuthenticatedState(
  page: Page,
  password: string = TEST_PASSWORD
): Promise<void> {
  await waitForRPCReady(page)

  // Give the app time to fully initialize (RPC connect + daemon poll)
  await page.waitForTimeout(8_000)

  await page.evaluate(async (pwd) => {
    const parade = (window as any).__parade
    if (!parade?.auth) throw new Error('Parade debug harness not found')
    await parade.auth.register(pwd)
    await parade.auth.login(pwd)
  }, password)
}

/**
 * Navigate using client-side router (preserves Pinia store state).
 * Use this instead of page.goto() after setupAuthenticatedState().
 */
export async function navigateTo(page: Page, path: string): Promise<void> {
  await page.evaluate((p) => {
    const router = (window as any).__parade_router
    if (router) {
      router.push(p)
    } else {
      // Fallback to hash/location
      window.location.hash = '#' + p
    }
  }, path)
  await page.waitForLoadState('networkidle')
}

/**
 * Create a team via the Pinia auth store.
 * Requires authenticated state first.
 */
export async function createTeamViaStore(
  page: Page,
  name: string = 'Test Team E2E',
  secret: string = 'test-secret-12345678901234567890'
): Promise<void> {
  await page.evaluate(async ({ name, secret }) => {
    const parade = (window as any).__parade
    if (!parade?.auth) throw new Error('Parade debug harness not found')
    await parade.auth.joinTeamWithName(name, secret)
  }, { name, secret })
}
