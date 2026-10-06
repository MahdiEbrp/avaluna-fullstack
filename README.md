# Avaluna

A complete, **single full-stack application** — one Go binary serves a bilingual
storefront, an admin console and a 391-endpoint JSON API from the same process.

**Go 1.23 · HTMX · Tailwind CSS · SQLite** — no Node runtime, no CDN, no
separate frontend, no external services.

```bash
make run        # migrate + seed + serve on http://localhost:3000
```

| Surface | URL |
|---|---|
| Storefront (Persian RTL by default) | <http://localhost:3000/> |
| Admin console | <http://localhost:3000/admin> |
| JSON API | <http://localhost:3000/api/v1> |
| Route table | <http://localhost:3000/api/v1/_routes> |
| Error registry | <http://localhost:3000/api/v1/_errors> |
| Health | <http://localhost:3000/api/v1/health> |

### Demo accounts

| Role | Email | Password |
|---|---|---|
| super_admin | `admin@yourstore.com` | `Admin@12345` |
| vendor | `vendor1@yourstore.com` | `Vendor@12345` |
| customer | `ali@example.com` | `Customer@12345` |

---

## What is in the box

**31 modules, 391 endpoints**: auth & users · CMS · catalog · inventory · cart ·
checkout & orders · payments & wallet · shipping · discounts · tax · reviews ·
wishlist & compare · blog · multi-vendor · returns · media · notifications ·
search · admin dashboard · webhooks · advanced analytics · cron management ·
health · backup & maintenance · SEO · localization · loyalty & gift cards ·
subscriptions · import/export · queue · feature flags & A/B testing.

**24 scheduled jobs** with retry policies, per-job locking and run history,
controllable from `/admin/cron`.

**6 queue workers** (`webhook.deliver`, `email.send`, `invoice.render`,
`report.generate`, `search.reindex`, `media.optimize`) with exponential backoff
and panic recovery.

**Bilingual everywhere** — Persian (RTL) and English (LTR) for both the
storefront and the admin, with a header language switcher, paired database
columns and Persian digit/currency formatting.

**Sandbox payments** — ZarinPal, Stripe and Wallet simulators. No real money
moves; you settle or fail a payment from the order page.

**Full seed data** — 24 products, 8 categories, 6 brands, 2 vendors, 40 orders,
coupons, blog posts, reviews, subscriptions, gift cards and more, so every
module is testable the moment the server starts.

---

## Architecture

```
                      ┌─────────────────────────────────────────┐
  Browser ──HTTP──▶   │  one Go process, one binary             │
                      │                                         │
                      │  middleware chain                       │
                      │   request-id → recover → logger →       │
                      │   security headers → CORS → locale →    │
                      │   authenticate → api-key → maintenance  │
                      │   → rate limit                          │
                      │              │                          │
                      │      ┌───────┴────────┐                 │
                      │      ▼                ▼                 │
                      │  internal/app     internal/api          │
                      │  (HTML + HTMX)    (JSON, 31 modules)    │
                      │      │                │                 │
                      │      └──── in-process bridge ───▶       │
                      │        (HTML forms reuse API handlers)  │
                      │              │                          │
                      │      internal/core (DI container)       │
                      │       │        │         │              │
                      │   database   cron      queue            │
                      │   (SQLite)  (24 jobs) (6 handlers)      │
                      └─────────────────────────────────────────┘
```

The HTML layer never re-implements business logic. Checkout, login and
registration forms call the *same* handler the JSON API exposes through an
in-process bridge that replays cookies and status codes — so the storefront and
the API can never drift apart.

### Design decisions

* **SQLite in WAL mode, single writer, pure-Go driver** (`modernc.org/sqlite`) —
  no cgo, no external database, deterministic concurrency.
* **Money is always an integer in minor units.** No floats touch the money path.
* **`html/template` with contextual escaping**; raw HTML only through an
  explicit, reviewed helper.
* **Stdlib `http.ServeMux`** with Go 1.22 method+wildcard patterns — no router
  dependency.
* **Everything embedded** — templates and static assets ship inside the binary
  via `//go:embed`.

---

## Structure

```
.
├── cmd/server/              entrypoint: config → db → cron → queue → http
├── internal/
│   ├── api/                 JSON API: one file per domain + resource layer
│   │   ├── router.go        route table, RegisterAll, /_routes, /_errors
│   │   ├── resource.go      generic CRUD with column allow-lists
│   │   ├── helpers.go       shared decoding, validation and audit helpers
│   │   └── auth.go …        one file per domain (see the module map below)
│   ├── app/                 HTML + HTMX layer
│   │   ├── app.go           template engine, view dict, middleware chain
│   │   ├── routes.go        storefront / account / admin route table
│   │   ├── shop.go          home, shop, product, search, blog, pages
│   │   ├── cart.go          cart, checkout, order, sandbox payment
│   │   ├── account.go       login, register, account tabs
│   │   └── admin.go         dashboard, 19 sections, reports, cron, settings
│   ├── auth/                Argon2id, JWT, TOTP, HMAC, RBAC
│   ├── config/              env-driven configuration
│   ├── core/                DI container + settings/cache/notify helpers
│   ├── cron/                scheduler, parser, the 24 jobs
│   ├── database/            schema.sql, migrations, seed, helpers
│   ├── httpx/               response envelopes, error registry, decoding
│   ├── i18n/                locale negotiation, 185×2 key catalogue
│   ├── middleware/          the full chain
│   ├── queue/               durable job queue
│   ├── testsupport/         boots a seeded in-process app for tests
│   └── tools/dbcheck/       tiny SQL inspection helper
├── web/
│   ├── embed.go             //go:embed templates + static
│   ├── templates/           layout, 15 storefront pages, 5 admin pages
│   └── static/              app.css, htmx, alpine, product artwork
├── data/                    SQLite db, backups, uploads, invoices (runtime)
├── docs/PHASE_CHECKLIST.md  every phase, item by item, with test evidence
├── API_DOCUMENTATION.md     the full API reference
├── Makefile
└── staticcheck.conf
```

### Module map

The specification numbers its 31 modules; the code is named after the domain
each file serves, which is what Go expects and what you actually search for.
The number has not been lost — every registrar still declares it, so
`GET /api/v1/_routes` reports the module a route belongs to.

| # | Module | File | Registrar |
|---|---|---|---|
| 01 | Auth & Users | `auth.go` | `registerAuthModule` |
| 02 | CMS: pages, menus, settings, banners, FAQs | `cms.go` | `registerCMSModule` |
| 03 | Catalog: products, variants, categories, brands | `catalog.go` | `registerCatalogModule` |
| 04 | Inventory & Warehouses | `inventory.go` | `registerInventoryModule` |
| 05 | Cart | `cart.go` | `registerCartModule` |
| 06 | Checkout & Orders | `checkout.go` | `registerCheckoutModule` |
| 07 | Payments, Gateways & Wallet | `payments.go` | `registerPaymentsModule` |
| 08 | Shipping & Delivery | `shipping.go` | `registerShippingModule` |
| 09 | Discounts & Coupons | `discounts.go` | `registerDiscountsModule` |
| 10 | Tax | `tax.go` | `registerTaxModule` |
| 11 + 12 | Reviews, Ratings, Wishlist & Compare | `reviews.go` | `registerReviewsModule` |
| 13 | Blog | `blog.go` | `registerBlogModule` |
| 14 + 15 | Vendors, Returns & Refunds | `vendors.go` | `registerVendorsModule` |
| 16 | Media & Upload | `media.go` | `registerMediaModule` |
| 17 | Notifications | `notifications.go` | `registerNotifyModule` |
| 18 | Search & Filters | `catalog.go` | `registerCatalogModule` |
| 19 | Admin Dashboard | `admin.go` | `registerAdminModule` |
| 20 | Webhooks & Integrations | `integrations.go` | `registerWebhookModule` |
| 21 | Reports & Analytics | `reports.go` | `registerReportsModule` |
| 22 | Cron Job management | `cron.go` | `registerCronModule` |
| 23 + 24 | Health, Monitoring, Backup, Cache, Maintenance | `system.go` | `registerHealthModule` |
| 25 | SEO & Sitemap | `seo.go` | `registerSEOModule` |
| 26 | Currency & Localization | `localization.go` | `registerLocaleModule` |
| 27 | Loyalty, Gift Cards & Referral | `loyalty.go` | `registerLoyaltyModule` |
| 28 | Subscriptions & Recurring Billing | `subscriptions.go` | `registerSubsModule` |
| 29 | Import/Export & Bulk Ops | `importexport.go` | `registerImportModule` |
| 30 | Queue Management | `queue.go` | `registerQueueModule` |
| 31 | Feature Flags & A/B Testing | `featureflags.go` | `registerFlagsModule` |

### Tests

| File | Covers |
|---|---|
| `internal/api/harness_test.go` | shared fixtures: envelope, `call`, `bearer`, `hammer`, DB scalars |
| `internal/api/contract_test.go` | envelope shape, RBAC, validation, module and error registries, security headers |
| `internal/api/auth_test.go` | lockout, bans, enumeration parity, 2FA, refresh rotation and reuse |
| `internal/api/integration_test.go` | cross-module journeys against real persisted state |
| `internal/api/routes_test.go` | sweeps every registered route |
| `internal/api/resource_test.go`, `services_test.go` | white-box tests of the resource and service layers |
| `internal/api/stress_service_test.go` | service-layer concurrency: wallet, stock, coupons, caches |
| `internal/api/stress_http_test.go` | HTTP-layer concurrency: checkout, idempotency, refresh, rate limiter |

Run the concurrency suite on its own with `make stress`, or under the race
detector with `make stress-race`.


---

## Quality gate

```bash
make check    # gofmt + go vet + staticcheck + go test
```

| Check | Result |
|---|---|
| `go build ./...` | pass |
| `go vet ./...` | pass, no findings |
| `staticcheck ./...` | pass, no findings |
| `gofmt -l cmd internal web` | empty |
| `go test ./...` | all packages ok |

Coverage: `app` 79.0 % · `auth` 85.7 % · `i18n` 89.4 % · `database` 78.6 % ·
`queue` 66.9 % · `cron` 62.1 % · `httpx` 55.6 %.

The suite boots a **real, fully seeded application** per test (temporary SQLite
file, real middleware chain, real templates) and drives it through
`httptest` — including a complete purchase: add to cart → coupon → checkout →
sandbox payment → order marked paid.

---

## Configuration

Everything is environment-driven with working defaults.

| Variable | Default | Purpose |
|---|---|---|
| `PORT` / `HOST` | `3000` / `0.0.0.0` | Listen address |
| `APP_ENV` | `development` | `production` → JSON logs + HSTS |
| `DATABASE_PATH` | `data/store.db` | SQLite file |
| `JWT_SECRET` | dev default | **set in production** |
| `ARGON_PEPPER` | dev default | **set in production** |
| `DEFAULT_LOCALE` | `fa` | `fa` or `en` |
| `DEFAULT_CURRENCY` | `IRR` | Display currency |
| `ENABLE_CRON` / `ENABLE_QUEUE` | `true` | Background subsystems |
| `QUEUE_CONCURRENCY` | `4` | Worker count |
| `AUTO_SEED` | `true` | Seed demo data on an empty database |

---

## Documentation

* **[`API_DOCUMENTATION.md`](API_DOCUMENTATION.md)** — envelope contract, error
  registry, auth & RBAC, rate-limit tiers, conventions, all 391 endpoints,
  worked request/response examples, the 24 cron jobs, the queue, and the
  security checklist.
* **[`docs/PHASE_CHECKLIST.md`](docs/PHASE_CHECKLIST.md)** — every phase marked
  done with the test that proves it.
