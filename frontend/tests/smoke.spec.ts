/**
 * Smoke tests — validate the full bridge: daemon proxy → WebSocket → mock → invoke → response.
 *
 * These tests verify that the entire E2E infrastructure works before
 * running the full test suite.
 */
import { test, expect, waitForRPCReady, registerIdentity } from './helpers'

test.describe('E2E Infrastructure Smoke', () => {
  test('S1: __TAURI_INTERNALS__ should be defined after page load', async ({ page }) => {
    await page.goto('/')
    await page.waitForLoadState('networkidle')

    const hasInternals = await page.evaluate(() => {
      return !!(window as any).__TAURI_INTERNALS__
    })
    expect(hasInternals).toBe(true)
  })

  test('S2: invoke check_has_identity should work (returns boolean)', async ({ page }) => {
    await page.goto('/')
    await waitForRPCReady(page)

    const result = await page.evaluate(async () => {
      const internals = (window as any).__TAURI_INTERNALS__
      return await internals.invoke('check_has_identity')
    })

    // Just verify it returns a boolean (may be true or false depending on test order)
    expect(typeof result).toBe('boolean')
  })

  test('S3: setup page should render at /setup when no identity exists', async ({ page }) => {
    await page.goto('/')
    await waitForRPCReady(page)

    // Navigate directly to setup page — the app doesn't auto-redirect from /
    await page.goto('/setup')
    await page.waitForLoadState('networkidle')

    // Should be on the setup page
    const heading = page.locator('h1')
    await expect(heading).toContainText('Create Your Identity')
  })

  test('S4: register a new identity via invoke', async ({ page }) => {
    await page.goto('/')
    await waitForRPCReady(page)

    // Register a new identity directly via RPC
    await registerIdentity(page, 'Test@123456')

    // Verify identity now exists
    const hasIdentity = await page.evaluate(async () => {
      const internals = (window as any).__TAURI_INTERNALS__
      return await internals.invoke('check_has_identity')
    })
    expect(hasIdentity).toBe(true)

    // Verify login works
    await page.evaluate(async () => {
      const internals = (window as any).__TAURI_INTERNALS__
      await internals.invoke('login', { password: 'Test@123456' })
    })
  })
})
