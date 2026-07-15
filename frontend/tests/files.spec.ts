import { test, expect } from './helpers'

test.describe('Files Flow (requires Tauri backend)', () => {
  test.skip('should require auth to access files page', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    await expect(page).toHaveURL(/\/(login|setup)/)
  })

  test.skip('should show files page structure after login', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    await expect(page.locator('.chat-view')).toBeVisible()
    await expect(page.locator('.left-sidebar')).toBeVisible()
  })

  test.skip('should have Files tab active by default', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const filesTab = page.locator('.panel-tab', { hasText: 'Files' })
    await expect(filesTab).toHaveClass(/active/)
  })

  test.skip('should switch between Files and Downloads tabs', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const downloadsTab = page.locator('.panel-tab', { hasText: 'Downloads' })
    await downloadsTab.click()
    await expect(downloadsTab).toHaveClass(/active/)
    
    const filesTab = page.locator('.panel-tab', { hasText: 'Files' })
    await filesTab.click()
    await expect(filesTab).toHaveClass(/active/)
  })

  test.skip('should have conversations section', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const conversationsSection = page.locator('.section-title', { hasText: 'Conversations' })
    await expect(conversationsSection).toBeVisible()
  })

  test.skip('should have peers section', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const peersSection = page.locator('.section-title', { hasText: 'Peers' })
    await expect(peersSection).toBeVisible()
  })

  test.skip('should show peer count', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const peerCount = page.locator('.peer-count')
    await expect(peerCount).toBeVisible()
  })

  test.skip('should browse shared files', async ({ page }) => {
    await page.goto('/files')
    await page.waitForLoadState('networkidle')
    
    const fileBrowser = page.locator('.file-browser')
    await expect(fileBrowser).toBeVisible()
  })
})
