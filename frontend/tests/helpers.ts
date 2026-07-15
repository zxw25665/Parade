import { test, expect, Page } from '@playwright/test'

const TEST_PASSWORD = 'Test@123456'
const STRONG_PASSWORD = 'MyStr0ng!Pass#2024'

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

export { expect, test }
