// specs/auth.spec.js – authentication flow tests
const { test, expect } = require("@playwright/test");

const ADMIN = { username: "admin", password: "admin123" };

// ── helpers ──────────────────────────────────────────────────────
async function login(page, username, password) {
  await page.goto("/login");
  await page.fill("#username", username);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
}

async function logout(page) {
  await page.click('button[type="submit"]:has-text("Sign out"), .sidebar-link--btn');
}

// ── tests ────────────────────────────────────────────────────────

test.describe("Login page", () => {
  test("shows login form and rejects unauthenticated access", async ({ page }) => {
    // Redirect / → /login
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);

    // Login page elements
    await expect(page.locator("#username")).toBeVisible();
    await expect(page.locator("#password")).toBeVisible();
    await expect(page.locator('button[type="submit"]')).toBeVisible();
  });

  test("shows error for wrong credentials", async ({ page }) => {
    await page.goto("/login");
    await page.fill("#username", "nobody");
    await page.fill("#password", "wrongpassword");
    await page.click('button[type="submit"]');
    await expect(page.locator(".flash--error")).toBeVisible();
  });

  test("redirects /chat to /login when not authenticated", async ({ page }) => {
    await page.goto("/chat");
    await expect(page).toHaveURL(/\/login/);
  });

  test("redirects /settings to /login when not authenticated", async ({ page }) => {
    await page.goto("/settings");
    await expect(page).toHaveURL(/\/login/);
  });

  test("redirects /admin to /login when not authenticated", async ({ page }) => {
    await page.goto("/admin");
    await expect(page).toHaveURL(/\/login/);
  });
});

test.describe("Registration", () => {
  const testUser = {
    username: `testuser_${Date.now()}`,
    password: "testpass123",
  };

  test("shows registration form", async ({ page }) => {
    await page.goto("/register");
    await expect(page.locator("#username")).toBeVisible();
    await expect(page.locator("#password")).toBeVisible();
    await expect(page.locator("#confirm_password")).toBeVisible();
  });

  test("rejects mismatched passwords", async ({ page }) => {
    await page.goto("/register");
    await page.fill("#username", "anyuser");
    await page.fill("#password", "pass1234");
    await page.fill("#confirm_password", "different");
    await page.click('button[type="submit"]');
    await expect(page.locator(".flash--error")).toBeVisible();
  });

  test("creates account and redirects to login", async ({ page }) => {
    await page.goto("/register");
    await page.fill("#username", testUser.username);
    await page.fill("#password", testUser.password);
    await page.fill("#confirm_password", testUser.password);
    await page.click('button[type="submit"]');
    await expect(page).toHaveURL(/\/login/);
    await expect(page.locator(".flash--success")).toBeVisible();
  });

  test("rejects duplicate username", async ({ page }) => {
    // Register the same username again
    await page.goto("/register");
    await page.fill("#username", testUser.username);
    await page.fill("#password", testUser.password);
    await page.fill("#confirm_password", testUser.password);
    await page.click('button[type="submit"]');
    await expect(page.locator(".flash--error")).toBeVisible();
  });
});

test.describe("Admin login", () => {
  test("admin can log in and reach /chat", async ({ page }) => {
    await login(page, ADMIN.username, ADMIN.password);
    await expect(page).toHaveURL(/\/chat/);
  });

  test("admin can log out", async ({ page }) => {
    await login(page, ADMIN.username, ADMIN.password);
    await page.waitForURL(/\/chat/);
    await logout(page);
    await expect(page).toHaveURL(/\/login/);
  });
});
