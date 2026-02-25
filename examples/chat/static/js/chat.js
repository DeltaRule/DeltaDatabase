/* chat.js – client-side behaviour for DeltaChat */

(function () {
  "use strict";

  // ── Auto-resize textarea ──────────────────────────────────────
  const textarea = document.getElementById("message-input");
  if (textarea) {
    textarea.addEventListener("input", () => {
      textarea.style.height = "auto";
      textarea.style.height = Math.min(textarea.scrollHeight, 160) + "px";
    });

    // Enter sends, Shift+Enter inserts newline
    textarea.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        const form = document.getElementById("message-form");
        if (form) form.dispatchEvent(new Event("submit", { cancelable: true }));
      }
    });
  }

  // ── Scroll messages to bottom ─────────────────────────────────
  function scrollToBottom() {
    const msgs = document.getElementById("messages");
    if (msgs) msgs.scrollTop = msgs.scrollHeight;
  }
  scrollToBottom();

  // ── Append a message bubble ───────────────────────────────────
  function appendMessage(role, content) {
    const msgs = document.getElementById("messages");
    if (!msgs) return;
    const wrapper = document.createElement("div");
    wrapper.className = `message message--${role}`;
    wrapper.innerHTML = `
      <span class="message-role">${role}</span>
      <div class="message-content">${escapeHtml(content)}</div>`;
    msgs.appendChild(wrapper);
    scrollToBottom();
  }

  function escapeHtml(str) {
    return str
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  // ── Send message ──────────────────────────────────────────────
  const form = document.getElementById("message-form");
  if (form) {
    form.addEventListener("submit", async (e) => {
      e.preventDefault();
      const input = document.getElementById("message-input");
      const sendBtn = document.getElementById("send-btn");
      const modelSelect = document.getElementById("model-select");
      const chatId = form.dataset.chatId;

      const message = input.value.trim();
      if (!message) return;

      // Optimistically show user message
      appendMessage("user", message);
      input.value = "";
      input.style.height = "auto";
      sendBtn.disabled = true;
      sendBtn.textContent = "…";

      try {
        const res = await fetch(`/chat/${chatId}/message`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            message,
            model: modelSelect ? modelSelect.value : "gpt-4o-mini",
          }),
        });

        const data = await res.json();

        if (!res.ok) {
          appendMessage("assistant", `⚠ Error: ${data.error || res.statusText}`);
        } else {
          appendMessage("assistant", data.reply);
          // Update sidebar title + page header if title changed
          if (data.title) {
            const sidebarTitle = document.getElementById(`title-${chatId}`);
            if (sidebarTitle) sidebarTitle.textContent = data.title;
            const headerTitle = document.getElementById("chat-header-title");
            if (headerTitle) headerTitle.textContent = data.title;
          }
        }
      } catch (err) {
        appendMessage("assistant", `⚠ Network error: ${err.message}`);
      } finally {
        sendBtn.disabled = false;
        sendBtn.textContent = "Send";
        input.focus();
      }
    });
  }

  // ── Delete chat ───────────────────────────────────────────────
  document.querySelectorAll(".chat-item-delete").forEach((btn) => {
    btn.addEventListener("click", async (e) => {
      e.preventDefault();
      e.stopPropagation();
      const chatId = btn.dataset.id;
      if (!confirm("Delete this chat?")) return;
      try {
        const res = await fetch(`/chat/${chatId}/delete`, { method: "POST" });
        if (res.ok) {
          const item = document.querySelector(`.chat-item[data-id="${chatId}"]`);
          if (item) item.remove();
          // If we deleted the active chat, go to /chat
          if (window.location.pathname.includes(chatId)) {
            window.location.href = "/chat";
          }
        }
      } catch (err) {
        alert(`Could not delete chat: ${err.message}`);
      }
    });
  });

  // ── Admin: save global models ─────────────────────────────────
  const saveGlobalBtn = document.getElementById("save-global-models");
  if (saveGlobalBtn) {
    saveGlobalBtn.addEventListener("click", async () => {
      const checked = Array.from(
        document.querySelectorAll("#global-models input[type=checkbox]:checked")
      ).map((cb) => cb.value);
      const res = await fetch("/admin/available-models", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ models: checked }),
      });
      if (res.ok) {
        saveGlobalBtn.textContent = "Saved ✓";
        setTimeout(() => (saveGlobalBtn.textContent = "Save global models"), 2000);
      }
    });
  }

  // ── Admin: save per-user models ───────────────────────────────
  document.querySelectorAll(".save-user-models").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const username = btn.dataset.username;
      const row = document.querySelector(`tr[data-username="${username}"]`);
      const checked = Array.from(
        row.querySelectorAll("input[type=checkbox]:checked")
      ).map((cb) => cb.value);
      const res = await fetch(`/admin/user/${username}/models`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ models: checked }),
      });
      if (res.ok) {
        btn.textContent = "Saved ✓";
        setTimeout(() => (btn.textContent = "Save"), 2000);
      }
    });
  });
})();
