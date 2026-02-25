// specs/settings.spec.js – user settings page tests
const { test, expect } = require("@playwright/test");

const ADMIN = { username: "admin", password: "admin123" };

async function login(page, username, password) {
  await page.goto("/login");
  await page.fill("#username", username);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/chat/);
}

test.describe("Settings", () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN.username, ADMIN.password);
  });

  test("navigates to settings from sidebar", async ({ page }) => {
    await page.click('a[href*="settings"]');
    await expect(page).toHaveURL(/\/settings/);
  });

  test("settings page shows API key and model fields", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.locator("#openai_api_key")).toBeVisible();
    await expect(page.locator("#openai_base_url")).toBeVisible();
    await expect(page.locator("#default_model")).toBeVisible();
  });

  test("can save settings and see success flash", async ({ page }) => {
    await page.goto("/settings");
    await page.fill("#openai_api_key", "sk-test-key-12345");
    await page.fill("#openai_base_url", "https://api.openai.com/v1");
    await page.click('button[type="submit"]');
    await expect(page.locator(".flash--success")).toBeVisible();
  });

  test("back link returns to chat", async ({ page }) => {
    await page.goto("/settings");
    await page.click('a:has-text("Back to chat")');
    await expect(page).toHaveURL(/\/chat/);
  });
});
