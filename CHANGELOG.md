# Changelog

All notable changes to DeltaDatabase are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
DeltaDatabase uses [Semantic Versioning](https://semver.org/).

---

## [Unreleased]

### Added
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

### Removed
- GitHub Actions workflow for deploying docs to GitHub Pages
  (documentation is now hosted on ReadTheDocs)
