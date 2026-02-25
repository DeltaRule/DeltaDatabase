// specs/admin.spec.js – admin panel tests
const { test, expect } = require("@playwright/test");

const ADMIN = { username: "admin", password: "admin123" };

async function login(page, username, password) {
  await page.goto("/login");
  await page.fill("#username", username);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/chat/);
}

// Register a fresh non-admin user for each test run
async function registerUser(page, username, password) {
  await page.goto("/register");
  await page.fill("#username", username);
  await page.fill("#password", password);
  await page.fill("#confirm_password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/login/);
}

test.describe("Admin panel", () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN.username, ADMIN.password);
  });

  test("admin link is visible in sidebar for admin user", async ({ page }) => {
    await expect(page.locator('a[href*="admin"]')).toBeVisible();
  });

  test("admin page shows users table and model controls", async ({ page }) => {
    await page.goto("/admin");
    await expect(page.locator("#users-table")).toBeVisible();
    await expect(page.locator("#global-models")).toBeVisible();
    await expect(page.locator("#save-global-models")).toBeVisible();
  });

  test("admin page lists the admin user", async ({ page }) => {
    await page.goto("/admin");
    await expect(page.locator("#users-table")).toContainText(ADMIN.username);
  });

  test("admin can save global model list", async ({ page }) => {
    await page.goto("/admin");
    // Uncheck all, then check the first model checkbox
    const checkboxes = page.locator("#global-models input[type=checkbox]");
    const count = await checkboxes.count();
    for (let i = 0; i < count; i++) {
      await checkboxes.nth(i).uncheck();
    }
    await checkboxes.first().check();
    const res = page.waitForResponse((r) => r.url().includes("/admin/available-models"));
    await page.click("#save-global-models");
    const response = await res;
    expect(response.status()).toBe(200);
  });

  test("non-admin user cannot access admin page", async ({ page }) => {
    const regularUser = `regular_${Date.now()}`;
    await registerUser(page, regularUser, "pass12345");
    await login(page, regularUser, "pass12345");

    await page.goto("/admin");
    // Should redirect to /chat (not admin)
    await expect(page).not.toHaveURL(/\/admin/);
  });

  test("admin link is hidden for non-admin user", async ({ page }) => {
    const regularUser2 = `regular2_${Date.now()}`;
    await registerUser(page, regularUser2, "pass12345");
    await login(page, regularUser2, "pass12345");

    // Admin link should not appear in sidebar
    const adminLink = page.locator('a[href*="admin"]');
    await expect(adminLink).toHaveCount(0);
  });
});
