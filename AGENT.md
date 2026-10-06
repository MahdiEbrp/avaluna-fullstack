# AGENT.md — Avaluna

## What it is

Single-binary Go application serving:

| Surface | URL |
|---|---|
| Storefront (fa RTL default, en LTR) | `/` |
| Admin console | `/admin` |
| JSON API, 31 modules | `/api/v1` |

Go 1.23 · SQLite (WAL, `modernc.org/sqlite`, no cgo) · HTMX + Alpine + Tailwind
· `html/template` · `//go:embed` · stdlib `http.ServeMux` (Go 1.22 patterns).
Not a SPA: server-rendered HTML with HTMX partial swaps.

## How it works (request flow)

1. `cmd/server/main.go` loads config, resolves/persists secrets
   (`data/.secrets.json` if env unset), opens SQLite, runs migrations + seed
   (`internal/database`), starts cron (`internal/cron`) and job queue
   (`internal/queue`), then serves HTTP.
2. Middleware chain (`internal/middleware`): request-id → recover → logger →
   security headers → CORS → locale → authenticate → api-key → maintenance →
   rate limit.
3. Routing: `internal/api/router.go` mounts the 31 JSON modules; `internal/app/routes.go`
   mounts storefront/account/admin HTML pages. HTML forms call the **same** API
   handlers through an in-process bridge (`internal/app` ↔ `internal/api`), so
   storefront and API never drift.
4. `internal/core` is the DI container wiring db, cron, queue, settings,
   cache, notifications.
5. Money is integer minor units everywhere. Payments are sandbox simulators.

## Read this file when you need that

| Task | Read |
|---|---|
| API endpoint inventory, request/response shapes | `API_DOCUMENTATION.md` |
| Project overview, features, demo accounts | `README.md` |
| Entrypoint / boot sequence | `cmd/server/main.go` |
| Route table, module registration, `/_routes`, `/_errors` | `internal/api/router.go` |
| One JSON domain's logic | `internal/api/<domain>.go` (auth, catalog, cart, …) |
| Generic CRUD columns/allow-lists | `internal/api/resource.go` |
| Storefront/admin HTML pages | `internal/app/{shop,cart,account,admin}.go` |
| Templates / static assets | `web/templates/`, `web/embed.go` |
| Auth (Argon2id, JWT, TOTP, RBAC) | `internal/auth/auth.go` |
| Configuration & every env var | `.env.example`, `internal/config/config.go` |
| DB schema, migrations, seed | `internal/database/{schema.sql,db.go,seed.go}` |
| Middleware order | `internal/middleware/middleware.go` |
| Cron jobs | `internal/cron/jobs.go` |
| Job queue workers | `internal/queue/queue.go` |
| i18n keys/locales | `internal/i18n/` |
| HTTP envelopes, error registry | `internal/httpx/response.go` |
| Boot a seeded test app | `internal/testsupport/testsupport.go` |

## Commands (necessary, performant, clean)

Use `make` targets; only these matter day to day:

```bash
make run      # dev: migrate + seed + serve on :3000
make build    # production binary → bin/server
make css      # rebuild Tailwind (needs .cache/toolchain/tailwindcss)
make test     # full test suite
make cover    # coverage summary
make vet      # go vet
make lint     # staticcheck (must be on PATH)
make check    # fmt + vet + lint + test — run before committing
```

Rules: `gofmt -l cmd internal web` must be empty; `make check` must pass;
do not commit `data/`, `bin/`, `/.cache/`, or generated `app.css`
(all gitignored). Budget: the `api` and `app` test packages are the slow ones
(~3 min each); run them once, not repeatedly.

## Notes for agents

- No code comments remain; rely on identifiers, tests (`*_test.go` next to each
  package) and this file.
- Comments/doc blocks were stripped intentionally — do not re-add them unless
  asked; keep `//go:embed`, `//nolint`, build tags intact.
- staticcheck is not installed system-wide:
  `go run honnef.co/go/tools/cmd/staticcheck@2025.1 ./...` (config:
  `staticcheck.conf`).
