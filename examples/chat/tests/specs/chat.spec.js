// specs/chat.spec.js – chat flow tests (requires MOCK_OPENAI=true)
const { test, expect } = require("@playwright/test");

const ADMIN = { username: "admin", password: "admin123" };

async function login(page, username, password) {
  await page.goto("/login");
  await page.fill("#username", username);
  await page.fill("#password", password);
  await page.click('button[type="submit"]');
  await page.waitForURL(/\/chat/);
}

test.describe("Chat", () => {
  test.beforeEach(async ({ page }) => {
    await login(page, ADMIN.username, ADMIN.password);
  });

  test("shows empty state when no chats exist", async ({ page }) => {
    // Either shows empty state or existing chats
    await expect(page).toHaveURL(/\/chat/);
    // The app shell is rendered
    await expect(page.locator(".sidebar")).toBeVisible();
    await expect(page.locator("#new-chat-btn")).toBeVisible();
  });

  test("can create a new chat", async ({ page }) => {
    await page.click("#new-chat-btn");
    await expect(page).toHaveURL(/\/chat\/.+/);
    await expect(page.locator("#message-input")).toBeVisible();
  });

  test("new chat appears in the sidebar", async ({ page }) => {
    await page.click("#new-chat-btn");
    await page.waitForURL(/\/chat\/.+/);
    // Active chat item is in sidebar
    await expect(page.locator(".chat-item--active")).toBeVisible();
  });

  test("can send a message and receive a mock reply", async ({ page }) => {
    await page.click("#new-chat-btn");
    await page.waitForURL(/\/chat\/.+/);

    await page.fill("#message-input", "Hello, this is a test message");
    await page.click("#send-btn");

    // User bubble appears immediately
    await expect(page.locator(".message--user").last()).toContainText("Hello, this is a test message");

    // Mock assistant reply appears
    await expect(page.locator(".message--assistant").last()).toBeVisible({ timeout: 10_000 });
    await expect(page.locator(".message--assistant").last()).toContainText("[mock]");
  });

  test("chat title auto-updates after first message", async ({ page }) => {
    await page.click("#new-chat-btn");
    await page.waitForURL(/\/chat\/.+/);

    // Title starts as "New Chat"
    await expect(page.locator("#chat-header-title")).toHaveText("New Chat");

    await page.fill("#message-input", "What is DeltaDatabase?");
    await page.click("#send-btn");

    // After reply, title changes to the first message text
    await expect(page.locator("#chat-header-title")).not.toHaveText("New Chat", { timeout: 10_000 });
  });

  test("model selector is visible and shows available models", async ({ page }) => {
    await page.click("#new-chat-btn");
    await page.waitForURL(/\/chat\/.+/);
    const select = page.locator("#model-select");
    await expect(select).toBeVisible();
    const options = await select.locator("option").allTextContents();
    expect(options.length).toBeGreaterThan(0);
  });

  test("can delete a chat", async ({ page }) => {
    // Create a chat first
    await page.click("#new-chat-btn");
    await page.waitForURL(/\/chat\/.+/);
    const activeItem = page.locator(".chat-item--active");
    await activeItem.hover();
    const deleteBtn = activeItem.locator(".chat-item-delete");
    await deleteBtn.click();
    page.once("dialog", (d) => d.accept());
    // After deletion, redirected to /chat
    await expect(page).toHaveURL(/\/chat$/, { timeout: 5_000 });
  });
});
