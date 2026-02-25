# DeltaChat – Example Chat Application

A full-stack AI chat application that uses **DeltaDatabase** as its only storage
backend. Built with [Flask](https://flask.palletsprojects.com/) and tested with
[Playwright](https://playwright.dev/).

## Features

| Feature | Details |
|---|---|
| 🔐 Authentication | Session-based login/register; every page except `/login` and `/register` requires authentication |
| 💬 Per-user chats | Chat sessions are isolated per user — nobody can read another user's conversations |
| ⚙ User settings | Each user stores their own OpenAI API key, base URL, and default model |
| 🛡 Admin panel | Admin users see all registered users and can assign specific models to each |
| 🤖 OpenAI backend | Only OpenAI-compatible APIs are supported (key + optional custom base URL) |
| 🗄 DeltaDatabase | All data (users, chats, config) is stored exclusively in DeltaDatabase |
| 🧪 Playwright tests | End-to-end browser tests covering auth, chat, settings, and admin flows |

## Quick Start

### Option A – Docker Compose (recommended)

```bash
cd examples/chat
docker compose up --build
```

Open **http://localhost:5000**.  Default admin credentials: `admin` / `admin123`.

> Set `MOCK_OPENAI=true` to get stub AI replies without a real API key:
>
> ```bash
> MOCK_OPENAI=true docker compose up --build
> ```

### Option B – Local Python

1. **Start DeltaDatabase** (all-in-one):
   ```bash
   docker compose -f ../../deploy/docker-compose/docker-compose.all-in-one.yml up -d
   ```

2. **Install Python deps**:
   ```bash
   pip install -r requirements.txt
   ```

3. **Run the app**:
   ```bash
   DELTA_DB_URL=http://localhost:8080 \
   MOCK_OPENAI=true \
   python app.py
   ```

4. Open **http://localhost:5000**

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `DELTA_DB_URL` | `http://localhost:8080` | DeltaDatabase REST endpoint |
| `DELTA_DB_CLIENT_ID` | `chat-app` | Client ID used to authenticate with DeltaDatabase |
| `FLASK_SECRET_KEY` | random | Flask session signing key — **set this in production** |
| `ADMIN_USERNAME` | `admin` | Username created on first run |
| `ADMIN_PASSWORD` | `admin123` | Password for the admin user — **change in production** |
| `MOCK_OPENAI` | `false` | If `true`, returns stub replies instead of calling OpenAI |
| `PORT` | `5000` | HTTP port for the Flask app |

## DeltaDatabase Schema

All data is stored as JSON entities across five logical databases:

| Database | Key | Contents |
|---|---|---|
| `chat_users` | `<username>` | `password_hash`, `is_admin`, `created_at` |
| `chat_sessions` | `<username>__<chat_id>` | Full message history + title + timestamps |
| `chat_index` | `<username>` | Ordered list of chat IDs for that user |
| `chat_user_config` | `<username>` | OpenAI API key, base URL, default model |
| `chat_admin_config` | `global` | Global available models + per-user overrides |
| `chat_admin_config` | `users_index` | Ordered list of all registered usernames |

## Running Playwright Tests

The tests require the chat application to be running with `MOCK_OPENAI=true`.

```bash
# 1. Start the stack
MOCK_OPENAI=true docker compose up -d --build

# 2. Install test dependencies
cd tests
npm install
npx playwright install --with-deps chromium

# 3. Run tests
npm test
```

Test suites:

| File | Covers |
|---|---|
| `specs/auth.spec.js` | Login, registration, protected routes, logout |
| `specs/chat.spec.js` | New chat, send message, mock reply, title update, delete |
| `specs/settings.spec.js` | Open settings, save API key/model, back navigation |
| `specs/admin.spec.js` | Admin page visibility, model assignment, non-admin access denied |

## Project Structure

```
examples/chat/
├── app.py                 # Flask application (routes, DeltaDatabase client)
├── requirements.txt
├── Dockerfile
├── docker-compose.yml
├── static/
│   ├── css/style.css      # Dark-theme UI styles
│   └── js/chat.js         # Client-side chat logic
├── templates/
│   ├── base.html
│   ├── login.html
│   ├── register.html
│   ├── chat.html          # Chat interface + sidebar
│   ├── settings.html      # User settings page
│   └── admin.html         # Admin panel
└── tests/
    ├── package.json
    ├── playwright.config.js
    └── specs/
        ├── auth.spec.js
        ├── chat.spec.js
        ├── settings.spec.js
        └── admin.spec.js
```
