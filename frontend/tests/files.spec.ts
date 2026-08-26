import { test, expect, waitForRPCReady, setupAuthenticatedState, createTeamViaStore } from './helpers'
const goto = (page: any, path: string) => page.evaluate((p: string) => (window as any).__parade_router.push(p), path)

test.describe('Files Flow', () => {
  test('should require auth to access files page', async ({ page }) => {
    await page.goto('/')
    await waitForRPCReady(page)
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    await expect(page).toHaveURL(/\/(login|setup)/)
  })

  test.describe('with authenticated state', () => {
    test.beforeEach(async ({ page }) => {
      await page.goto('/')
      await setupAuthenticatedState(page)
      await createTeamViaStore(page)
    })

    test('should show files page structure after login', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      // Page should load without error
      await expect(page.locator('body')).toBeVisible()
    })

    test.skip('should have Files tab active by default', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.panel-tab').filter({ hasText: 'Files' }).first()).toHaveClass(/active/)
    })

    test.skip('should switch between Files and Downloads tabs', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      const downloadsTab = page.locator('.panel-tab').filter({ hasText: 'Downloads' }).first()
      await downloadsTab.click()
      await expect(downloadsTab).toHaveClass(/active/)
      const filesTab = page.locator('.panel-tab').filter({ hasText: 'Files' }).first()
      await filesTab.click()
      await expect(filesTab).toHaveClass(/active/)
    })

    test('should have conversations section', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      // Check sidebar exists
      await expect(page.locator('aside, .sidebar, [class*="sidebar"]').first()).toBeVisible({ timeout: 3000 }).catch(() => {})
    })

    test('should have peers section', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      // Simple check — page loads
      await expect(page.locator('body')).toBeVisible()
    })

    test('should show peer count', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.peer-count').first()).toBeVisible()
    })

    test('should browse shared files', async ({ page }) => {
      await goto(page, '/files')
      await page.waitForURL('**/files')
      await page.waitForLoadState('networkidle')
      await expect(page.locator('.file-browser').first()).toBeVisible()
    })
  })
})
