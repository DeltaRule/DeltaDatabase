# Changelog

All notable changes to DeltaDatabase are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
DeltaDatabase uses [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### Added
- **Redesigned Management UI** — the built-in web UI has been rewritten as a multi-page application:
  - `index.html` — standalone login page with gradient background, password visibility toggle, and dev-mode support.
  - `app.html` — full management SPA with sidebar navigation, top bar, and page routing in vanilla JS.
  - `css/delta.css` — a self-contained design system (dark theme, custom properties, responsive grid); no CDN dependencies.
- **`GET /api/databases`** — new authenticated endpoint that returns a sorted list of database names currently held in the entity cache. Requires `read` permission.
- **`GET /api/me`** — new authenticated endpoint that returns the caller's `client_id`, full `permissions` array, and `is_admin` flag. Useful for building permission-aware UIs.
- **Database dropdown** on the Databases and Entities pages, populated from `GET /api/databases`.
- **Database cards** — clickable cards on the Databases page that open the Entities page pre-filtered for the selected database.
- **Mobile-responsive layout** — sidebar collapses on screens < 768 px; a hamburger button opens it as an overlay.
- **Explorer quick links** for `GET /api/databases` and `GET /api/me`.
- Tests for `handleDatabases` and `handleMe` covering auth, method rejection, and data correctness.

---

## [0.1.0-alpha.1] — 2026-02-25

> ⚠️ **Pre-release / Alpha** — APIs and storage formats may change without notice.

### Added
- **Admin key authentication** — start the Main Worker with `-admin-key` (or `$ADMIN_KEY` env var)
  for a single master credential that bypasses all RBAC checks, analogous to a PostgreSQL superuser
  password or MinIO root access key.
- **Persistent RBAC API keys** (`dk_…` prefix) — managed via `POST /api/keys`, `GET /api/keys`,
  and `DELETE /api/keys/{id}`. Keys are persisted to `<shared-fs>/_auth/keys.json` and survive
  restarts. Each key carries configurable `read`, `write`, and/or `admin` permissions with optional
  expiry.
- **Three-tier authentication priority** — every Bearer token is now evaluated as: admin key →
  API key → session token (from `POST /api/login`). All three types are usable directly without
  a login step.
- **Session token permissions now correctly inherited** — session tokens issued by `POST /api/login`
  carry the exact permissions of the admin key or API key used to authenticate. Previously, session
  tokens were always restricted to `read+write` even when the login credential had admin permissions.
- **Management UI — API Keys tab** — create, list, and delete RBAC API keys from the browser.
- **Management UI — Schema Export** — generate typed Pydantic (Python) or TypeScript interfaces
  from any JSON Schema loaded in the editor.
- **Management UI login** — the login screen now accepts an admin key or API key instead of a plain
  `client_id`. The `client_id` field is retained for backwards-compatible dev-mode only.
- **Chat example application** (`examples/chat/`) — a full-stack Flask chat app
  backed exclusively by DeltaDatabase, featuring:
  - Session-based authentication with login, registration, and logout
  - Per-user encrypted chat histories stored in DeltaDatabase
  - Per-user OpenAI API configuration (key, base URL, default model)
  - Admin panel for managing users and assigning allowed models per user
  - Support for custom OpenAI-compatible API endpoints
  - Mock mode (`MOCK_OPENAI=true`) for running without a real API key
  - Playwright end-to-end test suite covering auth, chat, settings, and admin
  - Docker Compose setup for one-command local deployment
- **ReadTheDocs documentation link** added to `README.md`
  (<https://deltadatabase.readthedocs.io/en/latest/>)
- **Changelog** (`CHANGELOG.md`) referenced from the documentation
- **Management UI Guide** (`docs/usage/frontend.md`) — documentation page with screenshots
  of every UI tab and detailed usage instructions.

### Fixed
- **`GET /api/keys` empty-state** — when no API keys exist the endpoint now returns `200 []`
  instead of `401`/`403`, so the Management UI shows "No API keys found." rather than
  "Failed to load keys" on a fresh install or after all keys are deleted.
- **`docker-compose.one-main-multiple-workers.yml`** — the `ADMIN_KEY` environment variable was
  missing from the `main-worker` service. It is now passed through correctly so that the admin key
  set in `.env` or the host environment is honoured in multi-worker deployments.
- **Session token permissions** — `extractBearerToken` now reads the roles stored on the session
  token rather than hard-coding `read+write`. This fixes the Management UI's Workers and API Keys
  tabs returning HTTP 403 when the user logged in with an admin key.

### Changed
- **`POST /api/login` request body** — the `key` field (admin key or API key) is now the primary
  authentication credential. The `client_id` field is still accepted for backwards compatibility
  when no admin key is configured (dev mode).
- **`POST /api/login` response** — the response now includes a `permissions` array so callers
  know which operations the issued token permits.

### Removed
- GitHub Actions workflow for deploying docs to GitHub Pages
  (documentation is now hosted on ReadTheDocs)

[0.1.0-alpha.1]: https://github.com/DeltaRule/DeltaDatabase/releases/tag/v0.1.0-alpha.1
