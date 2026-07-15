import { test, expect } from './helpers'

test.describe('Chat Flow (requires Tauri backend)', () => {
  test.skip('should require auth to access chat page', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    await expect(page).toHaveURL(/\/(login|setup)/)
  })

  test.skip('should show chat interface after login', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    await expect(page.locator('.chat-view')).toBeVisible()
  })

  test.skip('should show empty state when no conversation selected', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    await expect(page.locator('.empty-state')).toBeVisible()
    await expect(page.locator('.empty-title')).toContainText('Select a conversation')
  })

  test.skip('should have conversations section in sidebar', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    
    const sidebar = page.locator('.left-sidebar')
    await expect(sidebar).toBeVisible()
    
    const conversationsSection = page.locator('.section-title', { hasText: 'Conversations' })
    await expect(conversationsSection).toBeVisible()
  })

  test.skip('should have peers section in sidebar', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    
    const sidebar = page.locator('.left-sidebar')
    await expect(sidebar).toBeVisible()
    
    const peersSection = page.locator('.section-title', { hasText: 'Peers' })
    await expect(peersSection).toBeVisible()
  })

  test.skip('should have right panel with tabs', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    
    await expect(page.locator('.right-panel')).toBeVisible()
    
    const filesTab = page.locator('.panel-tab', { hasText: 'Files' })
    const downloadsTab = page.locator('.panel-tab', { hasText: 'Downloads' })
    
    await expect(filesTab).toBeVisible()
    await expect(downloadsTab).toBeVisible()
  })

  test.skip('should send team message', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    
    await page.locator('.conversation-item').first().click()
    
    const input = page.locator('.input-field')
    await input.fill('Hello from E2E test!')
    
    const sendBtn = page.locator('.send-btn')
    await sendBtn.click()
    
    await expect(page.locator('.message')).toContainText('Hello from E2E test!')
  })

  test.skip('should switch between team and private conversations', async ({ page }) => {
    await page.goto('/chat')
    await page.waitForLoadState('networkidle')
    
    const conversations = page.locator('.conversation-item')
    const count = await conversations.count()
    
    expect(count).toBeGreaterThan(0)
  })
})
