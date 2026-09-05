# konnyaku

Translation management server (in the spirit of Weblate / Crowdin) written in Go.

* Go 1.27 · Echo v5 · pgx v5 · sqlc · PostgreSQL 18
* Web UI: Vite + SolidJS + TypeScript, built into `web/dist` and embedded in the single binary.
* Designed to sit behind nginx, Caddy, or Cloudflare Tunnel (`PUBLIC_URL` must be the public origin).

## Features (current version)

| Area | Status |
| --- | --- |
| Locales (106 language-REGION codes seeded by default, e.g. `ja-JP`, `pt-BR`) / projects / components | ✅ |
| Users, admin flag, per-project roles (viewer / translator / manager) | ✅ |
| Catalog formats: JSON, YAML, gettext PO, Android `strings.xml` | ✅ (strings only; PO plurals and styled Android strings are rejected, not lost) |
| Import / export | ✅ (comments retained where supported; JSON is reformatted) |
| Translation UI: per-locale tabs with progress, server-side search, status filter, paging, optimistic locking | ✅ |
| Progress statistics per project / component / locale | ✅ |
| Translation history with word/character diffs and one-click restore, recorded by a database trigger | ✅ |
| Translation memory (pg_trgm similarity across the projects a user can access) with one-click bulk fill of exact matches | ✅ |
| Project glossary per locale, highlighted in source strings with a consistency check, CSV import/export | ✅ |
| Machine translation: OpenAI-compatible chat API, Google Cloud Translation v3 | ✅ |
| Repositories (project-level GitHub HTTPS remotes): clone / pull / sync / commit / push | ✅ |
| Translation file auto-detection in a checkout (`dir/{locale}.ext`, `dir/{locale}/file.ext`, `values-{locale}/strings.xml`); sync imports every locale file found, registering locales automatically, and tolerates `ja` vs `ja-JP`, `en_US` and Android `zh-rCN` spellings | ✅ |
| GitHub push webhook → queued re-import of every attached component | ✅ |
| "Publish translations": export on a fresh branch, push, open a draft pull request | ✅ |

## Quick start (development)

```bash
docker compose up -d                     # PostgreSQL 18 on 127.0.0.1:55432
cp .env.example .env                     # then export the variables (e.g. `set -a; . ./.env; set +a`)
make web                                 # builds the SPA into web/dist (needs Node 22+ and pnpm)
go run ./cmd/konnyaku migrate
ADMIN_EMAIL=admin@example.com ADMIN_PASSWORD='change-me-please' ADMIN_NAME=Admin go run ./cmd/konnyaku create-admin
go run ./cmd/konnyaku serve              # http://localhost:8080
```

Then in the UI: create a project (source locale) → connect a GitHub repository → **Clone** → **Detect translation files** → create components from the suggestions → **Sync from checkout** → translate → **Open draft pull request**. Components without a repository work with manual import/export instead.

For frontend development run `cd web && pnpm dev` (Vite on :5173, proxies `/api` to the Go server on :8080).

## Dev Container

Open the repository in VS Code and choose **Reopen in Container** (or run `devcontainer up --workspace-folder .`). The container ([.devcontainer](.devcontainer)) ships Go 1.27, Node 24 with pnpm, git and `psql`, plus a PostgreSQL 18 service with the `konnyaku` and `konnyaku_test` databases. After creation it downloads modules, builds the SPA, applies migrations and creates `admin@example.com` / `admin-password-123`; then run `make run` and open http://localhost:8080. `TEST_DATABASE_URL` is preset, so `make test` runs the integration tests too. Node modules live in a container-only volume so a host install with different native binaries does not interfere.  The feature-installed pnpm is pinned to the version in `web/package.json`; if they diverge, pnpm downloads itself into an ignored `.pnpm-store/` at the repository root, so bump both together.

If the `devcontainer` CLI hangs after the containers are up (it can wait forever for a Docker start event with some Docker Desktop versions), stop it and run the setup once by hand:

```bash
docker exec konnyaku_devcontainer-app-1 bash .devcontainer/post-create.sh
```

## Commands

| Command | Purpose |
| --- | --- |
| `konnyaku migrate` | Apply embedded SQL migrations (`db/migrations`), idempotent, advisory-locked |
| `konnyaku create-admin` | Create an administrator from `ADMIN_*` env vars |
| `konnyaku serve` | Run HTTP server + webhook worker |

## Configuration

See [.env.example](.env.example). `DATABASE_URL` is required; everything else is optional and the related feature returns a clear error when unset.

## HTTP API

All `/api/*` endpoints use a `session` cookie. Non-GET requests must send `X-Requested-With: konnyaku` (CSRF guard) and, if present, an `Origin` equal to `PUBLIC_URL`.

| Method & path | Role |
| --- | --- |
| `POST /api/login`, `POST /api/logout`, `GET /api/me` | – |
| `GET/POST /api/users` | admin |
| `GET/POST /api/locales`, `DELETE /api/locales/:code` | admin for writes |
| `GET/POST /api/projects`, `GET/PATCH/DELETE /api/projects/:id` | admin creates; manager edits |
| `GET /api/projects/:id/stats`, `GET /api/projects/:id/history` | viewer |
| `GET /api/projects/:id/locales`, `PUT/DELETE /api/projects/:id/locales/:code` | manager for writes |
| `GET/POST /api/projects/:id/repositories`, `GET/DELETE /api/repositories/:id` | admin creates/deletes |
| `GET /api/repositories/:id/scan` | manager |
| `POST /api/repositories/:id/git/:action` (`clone\|pull\|push\|sync\|commit`) | manager for `sync`, admin otherwise |
| `POST /api/repositories/:id/pull-request` `{title,body}` | admin |
| `GET/PUT/DELETE /api/projects/:id/members/:user` | manager |
| `GET/POST /api/projects/:id/components`, `GET/PATCH/DELETE /api/components/:id` | manager for writes |
| `GET /api/components/:id/stats`, `GET /api/components/:id/history?locale=` | viewer |
| `GET /api/components/:id/units?locale=&q=&status=&offset=` | viewer (50 per page, returns `{total,units}`) |
| `GET /api/units/:id/history?locale=` | viewer |
| `GET /api/units/:id/assist?locale=` (translation memory + glossary hits) | viewer |
| `GET/POST /api/projects/:id/glossary`, `DELETE /api/projects/:id/glossary/:term` | translator adds/updates, manager deletes |
| `GET /api/projects/:id/glossary/export?locale=`, `POST /api/projects/:id/glossary/import?locale=` (CSV body) | viewer / translator |
| `POST /api/components/:id/autofill` `{locale,status,dry_run}` (exact translation-memory matches into untranslated units) | translator |
| `POST /api/components/:id/import?locale=` (raw file body; returns `{imported,unknown,empty}`) | manager |
| `GET /api/components/:id/export?locale=` | viewer |
| `PUT /api/units/:id/translations/:locale` `{value,status,version}` | translator (`reviewed` needs manager) |
| `POST /api/units/:id/suggest` `{provider: openai\|google, locale}` | translator |
| `GET /api/deliveries`, `POST /api/deliveries/:id/retry` | admin |
| `POST /webhooks/github` (HMAC `X-Hub-Signature-256`) | GitHub |

## Development

```bash
make test        # unit tests (no database needed)
make sqlc        # regenerate internal/db after editing db/queries or db/migrations
make web         # rebuild the SPA (the Go binary embeds web/dist, so rebuild + restart after UI changes)
```

Integration tests for the server package run only when `TEST_DATABASE_URL` points to PostgreSQL 18. Each test creates and removes its own schema; the existing public schema is preserved. Use a development database.

Deployment examples (Docker, nginx, Caddy, Cloudflare Tunnel), integration setup, and current limitations are documented in [deploy/README.md](deploy/README.md).
