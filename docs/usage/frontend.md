# Management UI Guide

DeltaDatabase ships with a built-in browser-based Management UI available at **http://localhost:8080/**.  
This page walks through every screen with screenshots and explains how to use each feature.

---

## Login

![Login screen](https://github.com/user-attachments/assets/f91ea2aa-a97a-4635-900b-2332c43bf0b9)

When you open the UI you are presented with a login screen.

**Enter one of the following:**

| Credential | Description |
|---|---|
| Admin key | The value of `-admin-key` / `$ADMIN_KEY` — grants full access including key management |
| API key (`dk_…`) | A named RBAC key created via `POST /api/keys` — access limited to its configured permissions |

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

![Dashboard](https://github.com/user-attachments/assets/9669120a-76b2-46f8-9fda-8e1200852606)

The **Dashboard** is the first screen after login. It shows:

- **System Health** — live status from `GET /health`. A green **● OK** badge means the server is reachable.
- **Header status dot** — green when healthy, red when the server is unreachable.
- **Registered / Available Workers** — counts of Processing Workers known to the Main Worker.

The header always shows your current session identity (e.g. `👤 admin`) and a **Logout** button.

---

## Workers

![Workers page](https://github.com/user-attachments/assets/62ca11ae-46de-4b7b-8c01-7bcdcdcdff34)

The **Workers** tab lists all registered Processing Workers returned by `GET /admin/workers`.

> Requires **admin** permission.

| Column | Description |
|---|---|
| Worker ID | Unique identifier set with `-worker-id` when starting the proc-worker |
| Status | `Available` (green) or `Deallocating` (red) |
| Key ID | The master key ID the worker is currently using |
| Last Seen | Timestamp of the last successful subscription |
| Tags | Arbitrary key-value metadata (e.g. `grpc_addr`) |

Use the **↻ Refresh** button to reload the list at any time.

---

## Entities

![Entities page](https://github.com/user-attachments/assets/5a5c702c-56bb-4760-946c-97010259381c)

The **Entities** tab lets you read and write JSON entities directly from the browser.

### Get Entity

1. Enter the **Database** name (e.g. `chatdb`).
2. Enter the **Key** (entity key inside that database, e.g. `session_001`).
3. Click **GET**.

The entity's JSON document is displayed below the form on success, or an error badge on failure.

### Put Entity

1. Enter the **Database** name.
2. Enter the **JSON Body** — a JSON object where each top-level key is an entity key and its value is the entity document.  
   Example: `{"session_001": {"messages": [{"role":"user","content":"Hello"}]}}`
3. Click **PUT**.

Multiple entities can be written in a single PUT by including multiple top-level keys.

---

## Schemas

![Schemas page](https://github.com/user-attachments/assets/00832ac7-16dc-4f62-b9c1-e5a0c372551b)

The **Schemas** tab manages [JSON Schema](https://json-schema.org/) templates used to validate entity data.

### Available Schemas

Lists all schema IDs currently registered (from `GET /admin/schemas`). Click **Edit** next to any schema to load it into the editor.

### Create / Edit Schema

1. Enter a **Schema ID** (e.g. `chat.v1` or `product.v1`).
2. Paste or write a **JSON Schema draft-07** document in the editor.
3. Click **Load** to fetch an existing schema from the server into the editor.
4. Click **Save** to write the schema to the server (`PUT /schema/{id}`).

### Export Schema

Once a schema is loaded in the editor you can generate typed models from it:

- **🐍 Export as Pydantic** — generates a Python `BaseModel` class file ready to use with [Pydantic v2](https://docs.pydantic.dev/).
- **🔷 Export as TypeScript** — generates a TypeScript `interface` declaration file.

Click **Copy** to copy the generated code to the clipboard, or **⬇ Download** to save it as a file.

---

## API Keys

![API Keys page](https://github.com/user-attachments/assets/31410a76-25b7-4061-ade8-92031333c319)

The **API Keys** tab manages persistent RBAC API keys backed by `POST /api/keys` and `DELETE /api/keys/{id}`.

> Requires **admin** permission.

### Create New Key

| Field | Description |
|---|---|
| Name | A human-readable label (e.g. `ci-deploy`) |
| Permissions | Select one or more: `read`, `write`, `admin` |
| Expires In | Optional duration: `24h`, `7d`, `30d`, etc. Leave blank for non-expiring. |

Click **Create Key**. The raw secret (`dk_…`) is displayed **once only** — copy it immediately.

!!! warning
    The secret is never stored in plaintext and cannot be retrieved again after you close or refresh the page.

### Existing Keys

Lists all API keys with their ID, permissions, creation date, expiry, and enabled status. Click **Delete** to permanently remove a key.

---

## Explorer

![Explorer page](https://github.com/user-attachments/assets/e6ef9c4a-ca84-4231-953e-ddde05938490)

The **Explorer** tab is a lightweight HTTP client for testing any API endpoint.

### Raw Request

1. Select the **Method** (`GET` or `PUT`).
2. Enter the **Path** (e.g. `/health`, `/entity/mydb?key=hello`).
3. For PUT requests, enter a **JSON Body**.
4. Click **Send**.

The response status, HTTP code, latency in milliseconds, and body are shown below the form.

### Quick Endpoints

Pre-built buttons to quickly invoke common endpoints:

| Button | Endpoint |
|---|---|
| GET /health | Checks server health (no auth required) |
| GET /admin/workers | Lists all registered Processing Workers |

---

## Logging Out

Click the **Logout** button in the top-right corner at any time. This clears the session token from memory and returns you to the login screen. The token is not explicitly revoked on the server — it will naturally expire after the configured TTL.
