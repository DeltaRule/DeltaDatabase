# Management UI Guide

DeltaDatabase ships with a built-in browser-based Management UI available at **http://localhost:8080/**.  
The UI is a multi-page application served entirely by the Main Worker — no external dependencies or build step required.

---

## Login

![Login screen](https://github.com/user-attachments/assets/bcba6cbc-61a1-4377-9b6f-455153edea53)

When you open the UI you are presented with a login screen.

**Enter one of the following:**

| Credential | Description |
|---|---|
| Admin key | The value of `-admin-key` / `$ADMIN_KEY` — grants full access including key management |
| API key (`dk_…`) | A named RBAC key created via `POST /api/keys` — access limited to its configured permissions |
| Client ID (dev mode) | A plain client name — only works when no admin key is configured |

A short-lived **session token** is issued behind the scenes; it inherits the exact permissions of the credential you entered.

**Example — log in with the admin key:**

```bash
# The UI calls this automatically when you click Login
curl -s -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"key":"YOUR_ADMIN_KEY"}'
```

---

## Dashboard

![Dashboard](https://github.com/user-attachments/assets/82004499-a0f7-49f5-9ee6-79c9ff893e2f)

The **Dashboard** is the first screen after login. It shows:

- **Health** — live status from `GET /health`. A green **● Online** indicator means the server is reachable.
- **Available Workers** — number of Processing Workers currently available for routing.
- **Databases** — count of databases currently in the in-memory cache.
- **API Keys** — total number of configured API keys.
- **System Health** — key-value breakdown of all fields returned by `/health`.
- **Cache Statistics** — LRU cache entries, fill level, hits, misses, and evictions.

The header always shows your current session identity (e.g. `👑 admin`) and a **Refresh** button.

---

## Databases

![Databases page](https://github.com/user-attachments/assets/afb838e5-2018-4417-b21f-41cbdb723b8a)

The **Databases** tab lists all databases that have at least one entity in the in-memory cache. Powered by the new `GET /api/databases` endpoint.

### Database Cards & Dropdown

- **Database dropdown** — select any database from the dropdown to jump directly to the Entities page pre-filtered for that database.
- **Database cards** — click any card to open the Entities page for that database immediately.

### Create New Database

Databases are created automatically when you write the first entity. Use the form to write an initial entity:

| Field | Description |
|---|---|
| Database Name | The new database name (e.g. `users`) |
| Entity Key | The first entity key inside the database |
| JSON Body | A valid JSON object for the entity value |

Click **Write Entity**. The database card appears after the next refresh.

---

## Entities

The **Entities** tab lets you read and write JSON entities directly from the browser.

### Get Entity

1. Select a **Database** from the dropdown (populated from `GET /api/databases`).
2. Enter the **Entity Key**.
3. Click **GET Entity**.

The entity's JSON document is displayed below on success.

### Put Entity

1. Select or type a **Database** name — you can pick from the dropdown or type a new name in the custom field.
2. Enter the **JSON Body** — a JSON object where each top-level key is an entity key.  
   Example: `{"session_001": {"messages": [{"role":"user","content":"Hello"}]}}`
3. Click **PUT Entity**.

---

## Workers

The **Workers** tab lists all registered Processing Workers returned by `GET /admin/workers`.

> Requires **admin** permission.

| Column | Description |
|---|---|
| Worker ID | Unique identifier |
| Status | `available` (green) or other (red) |
| Key ID | The master key ID the worker is using |
| Last Seen | Timestamp of the last successful subscription |
| Tags | Arbitrary key-value metadata |

---

## Schemas

The **Schemas** tab manages JSON Schema templates.

### Schema List

Lists all schema IDs from `GET /admin/schemas`. Click any item to load it into the editor.

### Editor

1. Enter a **Schema ID** (e.g. `user.v1`).
2. Paste or write a **JSON Schema draft-07** document.
3. Click **Load** to fetch from server, **Save** to persist.

### Export

- **🐍 Pydantic** — generates a Python `BaseModel` class.
- **🔷 TypeScript** — generates a TypeScript `interface`.

Click **Copy** to copy the generated code.

---

## API Keys

![API Keys page](https://github.com/user-attachments/assets/8076e9ca-77e0-42d6-a1c0-c3a3250a7619)

The **API Keys** tab manages persistent RBAC API keys.

> Requires **admin** permission. Non-admin users see a lock screen.

### Create New Key

| Field | Description |
|---|---|
| Name | Human-readable label (e.g. `ci-deploy`) |
| Permissions | Check `read`, `write`, and/or `admin` |
| Expires In | Optional duration: `24h`, `7d`, `30d`. Leave blank for non-expiring. |

Click **Create Key**. The raw secret (`dk_…`) is displayed **once only** — copy it immediately.

!!! warning
    The secret is never stored in plaintext and cannot be retrieved again after you close or refresh the page.

### Existing Keys

Lists all API keys with their ID, permissions, expiry, and status. Click **Delete** to permanently remove a key.

---

## Explorer

The **Explorer** tab is a lightweight HTTP client for testing any API endpoint.

### Raw Request

1. Select the **Method** (`GET`, `PUT`, `POST`, `DELETE`).
2. Enter the **Path** (e.g. `/health`).
3. For write requests, enter a **JSON Body**.
4. Click **Send**.

The response status, latency, and body are shown below.

### Quick Endpoints

Pre-built buttons to quickly invoke common endpoints:

| Button | Endpoint |
|---|---|
| GET /health | Checks server health |
| GET /admin/workers | Lists registered Processing Workers |
| GET /api/databases | Lists all databases (cached) |
| GET /api/me | Returns current user identity and permissions |
| GET /api/keys | Lists API keys |

---

## Frontend-Specific Authenticated APIs

The UI uses two new endpoints designed specifically for the frontend:

### GET /api/databases

Returns a sorted JSON array of database names currently held in the entity cache. Requires **read** permission.

```bash
curl -s http://localhost:8080/api/databases \
  -H 'Authorization: Bearer YOUR_TOKEN'
# → ["products","sessions","users"]
```

### GET /api/me

Returns the authenticated caller's identity and permissions. Useful for building permission-aware UIs.

```bash
curl -s http://localhost:8080/api/me \
  -H 'Authorization: Bearer YOUR_TOKEN'
# → {"client_id":"admin","permissions":["read","write","admin"],"is_admin":true}
```

---

## Mobile Support

The UI is fully responsive. On screens narrower than 768 px:

- The sidebar is hidden by default.
- A **☰ hamburger button** appears in the top-left to open/close the sidebar as an overlay.
- All cards, grids, and forms reflow to single-column layout.

---

## Logging Out

Click the **Sign Out** button in the sidebar at any time. This clears the session token from browser memory and returns you to the login screen. The token will naturally expire after the configured TTL (`-client-token-ttl`, default 24 h).
