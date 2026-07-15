import { test, expect } from './helpers'

test.describe('Auth Flow - Setup Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/setup')
    await page.waitForLoadState('networkidle')
  })

  test('should display setup page with title', async ({ page }) => {
    await expect(page.locator('h1')).toContainText('Create Your Identity')
  })

  test('should show password strength indicator', async ({ page }) => {
    const passwordInput = page.locator('#password')
    await passwordInput.fill('weak')
    
    const strengthLabel = page.locator('.strength-label')
    await expect(strengthLabel).toBeVisible()
    
    await passwordInput.fill('VeryStr0ng!Pass#2024')
    await expect(strengthLabel).toContainText(/Good|Strong/)
  })

  test('should show password requirements', async ({ page }) => {
    const requirements = page.locator('.requirement')
    await expect(requirements).toHaveCount(4)
    
    await expect(page.locator('.requirement', { hasText: 'At least 8 characters' })).toBeVisible()
    await expect(page.locator('.requirement', { hasText: 'Uppercase & lowercase letters' })).toBeVisible()
    await expect(page.locator('.requirement', { hasText: 'At least one number' })).toBeVisible()
    await expect(page.locator('.requirement', { hasText: 'Special character' })).toBeVisible()
  })

  test('should show password mismatch message', async ({ page }) => {
    const passwordInput = page.locator('#password')
    const confirmInput = page.locator('#confirm')
    
    await passwordInput.fill('Test@123456')
    await confirmInput.fill('Different@123')
    
    const mismatchMsg = page.locator('.mismatch')
    await expect(mismatchMsg).toBeVisible()
    await expect(mismatchMsg).toContainText("Passwords don't match")
  })

  test('should show password match message', async ({ page }) => {
    const passwordInput = page.locator('#password')
    const confirmInput = page.locator('#confirm')
    
    await passwordInput.fill('Test@123456')
    await confirmInput.fill('Test@123456')
    
    const matchMsg = page.locator('.match')
    await expect(matchMsg).toBeVisible()
    await expect(matchMsg).toContainText('Passwords match')
  })

  test('should have back button', async ({ page }) => {
    const backBtn = page.locator('.back-btn')
    await expect(backBtn).toBeVisible()
  })

  test('should navigate back to home', async ({ page }) => {
    const backBtn = page.locator('.back-btn')
    await backBtn.click()
    await expect(page).toHaveURL(/\//)
    await expect(page.locator('h1.logo-text')).toHaveText('Parade')
  })
})

test.describe('Auth Flow - Login Page', () => {
  test.skip('should display login page with title when identity exists (requires Tauri backend)', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    await expect(page.locator('h1')).toContainText('Welcome Back')
  })

  test('should have password input', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    const passwordInput = page.locator('#password')
    await expect(passwordInput).toBeVisible()
  })

  test('should toggle password visibility', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    
    const passwordInput = page.locator('#password')
    const toggleBtn = page.locator('.toggle-btn').first()
    
    await passwordInput.fill('secret123')
    await expect(passwordInput).toHaveAttribute('type', 'password')
    
    await toggleBtn.click()
    await expect(passwordInput).toHaveAttribute('type', 'text')
    
    await toggleBtn.click()
    await expect(passwordInput).toHaveAttribute('type', 'password')
  })

  test('should navigate back to home', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')
    
    const backBtn = page.locator('.back-btn')
    await backBtn.click()
    await expect(page).toHaveURL(/\//)
  })
})

test.describe('Auth Flow - Home Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForLoadState('networkidle')
  })

  test('should display home page with logo', async ({ page }) => {
    await expect(page.locator('h1.logo-text')).toHaveText('Parade')
  })

  test('should display tagline', async ({ page }) => {
    await expect(page.locator('.tagline')).toContainText('Decentralized')
  })

  test('should display sub-tagline', async ({ page }) => {
    await expect(page.locator('.sub-tagline')).toContainText('End-to-end encrypted')
  })

  test('should display footer with version', async ({ page }) => {
    await expect(page.locator('.version')).toContainText('v1.0.0')
  })

  test('should display footer with tech info', async ({ page }) => {
    await expect(page.locator('.tech')).toContainText('Built with libp2p')
  })
})
