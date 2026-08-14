import { test, expect, waitForRPCReady, setupAuthenticatedState, createTeamViaStore } from './helpers'

const goto = (page: any, path: string) => page.evaluate((p: string) => (window as any).__parade_router.push(p), path)

test.describe('Chat Flow', () => {
  test('should require auth to access chat page', async ({ page }) => {
    await page.goto('/')
    await waitForRPCReady(page)
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    await expect(page).toHaveURL(/\/(login|setup)/)
  })

  test.describe('with authenticated state', () => {
    test.beforeEach(async ({ page }) => {
      await page.goto('/')
      await setupAuthenticatedState(page)
      await createTeamViaStore(page)
    })

    test('should show chat interface after login', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.chat-view')).toBeVisible()
    })

    test('should show empty state when no conversation selected', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.empty-state').first()).toBeVisible()
      await expect(page.locator('.empty-title')).toContainText('Select a conversation')
    })

    test('should have conversations section in sidebar', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.left-sidebar')).toBeVisible()
      await expect(page.locator('.section-title', { hasText: 'Conversations' })).toBeVisible()
    })

    test('should have peers section in sidebar', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.left-sidebar')).toBeVisible()
      await expect(page.locator('.section-title', { hasText: 'Peers' })).toBeVisible()
    })

    test('should have right panel with tabs', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.right-panel').first()).toBeVisible()
      await expect(page.locator('.panel-tab', { hasText: 'Files' })).toBeVisible()
      await expect(page.locator('.panel-tab', { hasText: 'Downloads' })).toBeVisible()
    })

    // Requires conversation with messages — conversation not auto-created
    test.skip('should send team message', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      await page.locator('.conversation-item').first().click()
      const input = page.locator('.input-field')
      await input.fill('Hello from E2E test!')
      const sendBtn = page.locator('.send-btn')
      await sendBtn.click()
      await expect(page.locator('.message')).toContainText('Hello from E2E test!')
    })

    // Requires multiple conversations — only one team exists
    test.skip('should switch between team and private conversations', async ({ page }) => {
      await goto(page, '/chat')
      await page.waitForURL('**/chat')
      await page.waitForLoadState('networkidle')
      const count = await page.locator('.conversation-item').count()
      expect(count).toBeGreaterThan(0)
    })
  })
})
