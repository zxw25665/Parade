import { test, expect } from './helpers'

test.describe('Team Flow (requires Tauri backend)', () => {
  test.skip('should show team join page with tabs after setup', async ({ page }) => {
    await page.goto('/team-join')
    await page.waitForLoadState('networkidle')
    
    await expect(page.locator('h1')).toContainText('Join a Team')
    await expect(page.locator('.tab', { hasText: 'Join Team' })).toBeVisible()
    await expect(page.locator('.tab', { hasText: 'Create Team' })).toBeVisible()
  })

  test.skip('should switch between join and create tabs', async ({ page }) => {
    await page.goto('/team-join')
    await page.waitForLoadState('networkidle')
    
    const joinTab = page.locator('.tab', { hasText: 'Join Team' })
    const createTab = page.locator('.tab', { hasText: 'Create Team' })
    
    await expect(joinTab).toHaveClass(/active/)
    
    await createTab.click()
    await expect(createTab).toHaveClass(/active/)
    await expect(page.locator('h1')).toContainText('Create Team')
    
    await joinTab.click()
    await expect(joinTab).toHaveClass(/active/)
    await expect(page.locator('h1')).toContainText('Join a Team')
  })

  test.skip('should have Join Team tab with optional name and required secret', async ({ page }) => {
    await page.goto('/team-join')
    await page.waitForLoadState('networkidle')
    
    await expect(page.locator('#join-name')).toBeVisible()
    await expect(page.locator('#join-secret')).toBeVisible()
    
    const joinBtn = page.locator('.submit-btn', { hasText: 'Join Team' })
    await expect(joinBtn).toBeDisabled()
    
    await page.locator('#join-secret').fill('secret123')
    await expect(joinBtn).toBeEnabled()
  })

  test.skip('should have Create Team tab with required name and secret', async ({ page }) => {
    await page.goto('/team-join')
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

  test.skip('should generate secret when clicking generate button', async ({ page }) => {
    await page.goto('/team-join')
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

  test.skip('should copy secret to clipboard', async ({ page, context }) => {
    await context.grantPermissions(['clipboard-read', 'clipboard-write'])
    
    await page.goto('/team-join')
    await page.waitForLoadState('networkidle')
    
    const createTab = page.locator('.tab', { hasText: 'Create Team' })
    await createTab.click()
    
    const generateBtn = page.locator('button', { hasText: 'Generate' })
    await generateBtn.click()
    
    const copyBtn = page.locator('button', { hasText: /^(Copy|Copied!)$/ })
    await copyBtn.click()
    
    await expect(copyBtn).toContainText('Copied!')
  })

  test.skip('should create and join a new team', async ({ page }) => {
    await page.goto('/team-join')
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

  test.skip('should join existing team with secret', async ({ page }) => {
    await page.goto('/team-join')
    await page.waitForLoadState('networkidle')
    
    await page.locator('#join-secret').fill('existing-team-secret-12345678901234567890')
    
    const joinBtn = page.locator('.submit-btn', { hasText: 'Join Team' })
    await joinBtn.click()
    
    await page.waitForURL(/\/chat/, { timeout: 30000 })
    expect(page.url()).toMatch(/\/chat/)
  })
})
