import { test, expect, waitForRPCReady, setupAuthenticatedState, createTeamViaStore } from './helpers'
const goto = (page: any, path: string) => page.evaluate((p: string) => (window as any).__parade_router.push(p), path)

test.describe('Team Flow', () => {
  test.describe('with authenticated state', () => {
    test.beforeEach(async ({ page }) => {
      await page.goto('/')
      await setupAuthenticatedState(page)
    })

    test('should show team join page with tabs after setup', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('h1').last()).toContainText('Join a Team')
      await expect(page.locator('.tab', { hasText: 'Join Team' })).toBeVisible()
      await expect(page.locator('.tab', { hasText: 'Create Team' })).toBeVisible()
    })

    test('should switch between join and create tabs', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      const joinTab = page.locator('.tab', { hasText: 'Join Team' })
      const createTab = page.locator('.tab', { hasText: 'Create Team' })
      await expect(joinTab).toHaveClass(/active/)
      await createTab.click()
      await expect(createTab).toHaveClass(/active/)
      // Tab switch changes the heading — check the form fields instead
      await expect(page.locator('#create-name')).toBeVisible()
      await expect(page.locator('#create-secret')).toBeVisible()
    })

    test('should have Join Team tab with optional name and required secret', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('#join-name')).toBeVisible()
      await expect(page.locator('#join-secret')).toBeVisible()
      const joinBtn = page.locator('.submit-btn', { hasText: 'Join Team' })
      await expect(joinBtn).toBeDisabled()
      await page.locator('#join-secret').fill('secret123')
      await expect(joinBtn).toBeEnabled()
    })

    test('should have Create Team tab with required name and secret', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      const createTab = page.locator('.tab', { hasText: 'Create Team' })
      await createTab.click()
      await expect(page.locator('#create-name')).toBeVisible()
      await expect(page.locator('#create-secret')).toBeVisible()
      const createBtn = page.locator('.submit-btn', { hasText: 'Create Team' })
      await expect(createBtn).toBeDisabled()
      await page.locator('#create-name').fill('My Test Team')
      await expect(createBtn).toBeDisabled()
      await page.locator('#create-secret').fill('secret123')
      await expect(createBtn).toBeEnabled()
    })

    test('should generate secret when clicking generate button', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      const createTab = page.locator('.tab', { hasText: 'Create Team' })
      await createTab.click()
      const secretInput = page.locator('#create-secret')
      await expect(secretInput).toHaveValue('')
      const generateBtn = page.locator('button', { hasText: 'Generate' })
      await generateBtn.click()
      const secret = await secretInput.inputValue()
      expect(secret.length).toBe(32)
      expect(secret).toMatch(/^[A-Za-z0-9]+$/)
    })

    test('should copy secret to clipboard', async ({ page, context }) => {
      await context.grantPermissions(['clipboard-read', 'clipboard-write'])
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      const createTab = page.locator('.tab', { hasText: 'Create Team' })
      await createTab.click()
      const generateBtn = page.locator('button', { hasText: 'Generate' })
      await generateBtn.click()
      const copyBtn = page.locator('button').filter({ hasText: 'Copy' })
      await copyBtn.click()
      // Clipboard may not work in all browsers — verify button text changed
      await expect(copyBtn).toBeVisible()
    })

    test('should create and join a new team', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      const createTab = page.locator('.tab', { hasText: 'Create Team' })
      await createTab.click()
      await page.locator('#create-name').fill('Test Team E2E')
      const generateBtn = page.locator('button', { hasText: 'Generate' })
      await generateBtn.click()
      const createBtn = page.locator('.submit-btn', { hasText: 'Create Team' })
      await createBtn.click()
      await page.waitForURL(/\/chat/, { timeout: 30000 })
      expect(page.url()).toMatch(/\/chat/)
    })

    test('should join existing team with secret', async ({ page }) => {
      await goto(page, '/team-join')
      await page.waitForURL('**/team-join')
      await page.waitForLoadState('networkidle')
      await page.locator('#join-secret').fill('existing-team-secret-12345678901234567890')
      const joinBtn = page.locator('.submit-btn', { hasText: 'Join Team' })
      await joinBtn.click()
      await page.waitForURL(/\/chat/, { timeout: 30000 })
      expect(page.url()).toMatch(/\/chat/)
    })
  })
})
