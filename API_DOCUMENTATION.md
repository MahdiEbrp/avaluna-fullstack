# API Documentation — Avaluna

**Version** `1.0.0` · **Base URL** `http://localhost:3000/api/v1` · **Stack** Go 1.23 · HTMX · Tailwind CSS · SQLite

This is a **single full-stack application**. One Go binary serves:

| Surface | Mount | Description |
|---|---|---|
| Storefront | `/` | Bilingual (Persian RTL / English LTR) server-rendered HTML + HTMX |
| Admin console | `/admin` | Staff dashboard, 19 data sections, 20 reports, cron & queue control |
| JSON API | `/api/v1` | 391 endpoints across 31 modules |
| Static assets | `/static` | Vendored Tailwind CSS, HTMX, Alpine — no CDN |
| SEO | `/sitemap.xml`, `/robots.txt` | Generated from live content |

---

## 1. Response Envelope

Every JSON response — success or failure — uses the same envelope.

### Success

```json
{
  "success": true,
  "statusCode": 200,
  "message": "OK",
  "data": { },
  "meta": {
    "pagination": {
      "page": 1,
      "limit": 20,
      "totalItems": 137,
      "totalPages": 7
    }
  },
  "timestamp": "2026-10-04T08:11:31Z"
}
```

`meta` is omitted when there is nothing to report. `meta.pagination` is present on
every list endpoint. `meta.extra` carries endpoint-specific metadata (report
titles, job counts, …).

### Error

```json
{
  "success": false,
  "statusCode": 422,
  "error": {
    "code": "OUT_OF_STOCK",
    "message": "One or more items are out of stock",
    "details": [
      { "field": "items[0].variantId", "issue": "only 2 left in stock" }
    ]
  },
  "timestamp": "2026-10-04T08:11:31Z"
}
```

`error.details` is an array of `{field, issue}` objects and is omitted when empty.

---

## 2. Error Code Registry

The registry is served live at `GET /api/v1/_errors`.

| Code | HTTP | Meaning |
|---|---|---|
| `VALIDATION_ERROR` | 400 | Request body or query failed validation |
| `UNAUTHORIZED` | 401 | Missing, malformed or expired credentials |
| `FORBIDDEN` | 403 | Authenticated but the role is insufficient |
| `NOT_FOUND` | 404 | Resource does not exist or is soft-deleted |
| `CONFLICT` | 409 | Unique constraint or state conflict |
| `OUT_OF_STOCK` | 422 | Requested quantity exceeds available stock |
| `COUPON_INVALID` | 422 | Coupon expired, exhausted or not applicable |
| `PAYMENT_FAILED` | 402 | Gateway declined or the wallet balance is short |
| `SHIPPING_UNAVAILABLE` | 422 | No method serves the destination |
| `RATE_LIMIT_EXCEEDED` | 429 | Tier quota exhausted — see `Retry-After` |
| `IDEMPOTENCY_CONFLICT` | 409 | Same `Idempotency-Key`, different payload |
| `CRON_JOB_NOT_FOUND` | 404 | Unknown job name |
| `CRON_JOB_ALREADY_RUNNING` | 409 | The job holds its lock |
| `BACKUP_IN_PROGRESS` | 409 | A snapshot is already running |
| `IMPORT_VALIDATION_FAILED` | 422 | CSV rows failed validation |
| `SUBSCRIPTION_PAYMENT_FAILED` | 402 | Recurring charge declined |
| `GIFT_CARD_ALREADY_REDEEMED` | 409 | Card already consumed |
| `FEATURE_FLAG_DISABLED` | 403 | The feature is switched off for this user |
| `MAINTENANCE_MODE` | 503 | Store is closed for maintenance |
| `INTERNAL_SERVER_ERROR` | 500 | Unhandled failure (logged with a request id) |

---

## 3. Authentication

### Tokens

| Token | Lifetime | Transport |
|---|---|---|
| Access (JWT HS256) | 15 minutes | `Authorization: Bearer <jwt>` **or** the `ecms_session` cookie |
| Refresh | 30 days | `ecms_refresh` HttpOnly cookie, rotated on every use, hashed at rest, grouped by `family` so theft detection can revoke a whole chain |

```http
POST /api/v1/auth/login
Content-Type: application/json

{ "email": "admin@yourstore.com", "password": "Admin@12345", "code": "123456" }
```

```json
{
  "success": true,
  "statusCode": 200,
  "data": {
    "tokens": {
      "accessToken": "eyJhbGciOiJIUzI1NiIs...",
      "tokenType": "Bearer",
      "expiresIn": 900,
      "refreshToken": "7f41adaa..."
    },
    "user": { "id": 7, "email": "ali@example.com", "role": "customer", "locale": "fa" }
  }
}
```

`code` is the 6-digit TOTP and is **required for `admin`, `super_admin` and
`vendor` accounts once 2FA is enabled** (`POST /auth/2fa/enable`).

Passwords are hashed with **Argon2id** (t=2, m=64 MiB, p=2, 32-byte key) plus a
server-side pepper.

### Roles (RBAC)

`guest` → `customer` → `vendor` → `editor` → `support` → `admin` → `super_admin`

Each endpoint declares a minimum role (the **Access** column in §6). `editor` and
above count as *staff* and may reach `/admin`.

---

## 4. Rate Limiting

Per client IP + scope, sliding window. Responses carry `X-RateLimit-Limit`,
`X-RateLimit-Remaining` and `Retry-After` when throttled.

| Tier | Quota | Applies to |
|---|---|---|
| Global | 300 / min | Every request |
| Auth | 10 / min | `/auth/login`, `/auth/register`, `/auth/forgot-password` |
| Checkout | 10 / min | `/checkout/complete`, payment capture |
| Write | 60 / min | All `POST` / `PUT` / `PATCH` / `DELETE` |
| Read | 120 / min | Public catalog and content reads |
| Admin | 600 / min | Staff endpoints |

---

## 5. Conventions

* **Idempotency** — financial endpoints (`/checkout/complete`, `/payments/*`,
  `/loyalty/redeem`, `/subscriptions/*`) accept `Idempotency-Key: <uuid>`. The
  key is claimed atomically, so exactly one request per key ever reaches the
  handler. A duplicate is answered one of two ways:

  | Situation | Response |
  |---|---|
  | The original has finished | its stored response, replayed verbatim for 24 hours, with `Idempotent-Replay: true` |
  | The original is still in flight | `409 IDEMPOTENCY_CONFLICT` — retry once it settles |
  | A different body reuses the key | `409 IDEMPOTENCY_CONFLICT` |

  A key whose request failed is released, so the client may retry it. Note
  that a replay repeats the original **status code** as well as the body: a
  replayed checkout returns `201`, not `200`. Detect replays with the
  `Idempotent-Replay` header rather than the status.
* **Money** is always an **integer in minor units** (Rial for `IRR`, cents for
  `USD`). No floats anywhere in the money path.
* **Timestamps** are ISO-8601 UTC strings (`2026-10-04T08:11:31Z`).
* **Pagination** — `?page=1&limit=20` (limit clamped to 100).
* **Sorting / filtering** — `?sort=price_asc|price_desc|newest|popular|rating`,
  plus per-module filters documented inline.
* **Soft delete** — rows carry `deleted_at`; deleted rows are invisible to reads.
* **Bilingual columns** — every translatable table has `x` (Persian) and `x_en`
  (English) columns; the API returns both and the UI picks by locale.
* **Locale negotiation** — `?lang=` → `ecms_locale` cookie → `Accept-Language` →
  default `fa`.
* **CSRF** — HTML form posts carry a double-submit `ecms_csrf` token; the JSON
  API is exempt because it requires a bearer token.

---

## 6. Endpoint Index

All 391 endpoints, grouped by module. Paths are relative to `/api/v1`
(shown in full below). The live machine-readable version is at
`GET /api/v1/_routes`.

### Module 01 — Authentication & Users

`20 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/auth/2fa/enable` | authenticated |
| `POST` | `/api/v1/auth/2fa/verify` | authenticated |
| `POST` | `/api/v1/auth/forgot-password` | public |
| `POST` | `/api/v1/auth/login` | public |
| `POST` | `/api/v1/auth/logout` | public |
| `POST` | `/api/v1/auth/refresh-token` | public |
| `POST` | `/api/v1/auth/register` | public |
| `POST` | `/api/v1/auth/reset-password` | public |
| `GET` | `/api/v1/auth/sessions` | authenticated |
| `POST` | `/api/v1/auth/verify-email` | public |
| `GET` | `/api/v1/users/me` | authenticated |
| `PATCH` | `/api/v1/users/me` | authenticated |
| `DELETE` | `/api/v1/users/me` | authenticated |
| `GET` | `/api/v1/users/me/addresses` | authenticated |
| `POST` | `/api/v1/users/me/addresses` | authenticated |
| `GET` | `/api/v1/users/me/addresses/{id}` | authenticated |
| `PUT` | `/api/v1/users/me/addresses/{id}` | authenticated |
| `PATCH` | `/api/v1/users/me/addresses/{id}` | authenticated |
| `DELETE` | `/api/v1/users/me/addresses/{id}` | authenticated |
| `GET` | `/api/v1/users/me/orders` | authenticated |

### Module 02 — CMS (Pages, Menus, Settings, Banners, FAQs)

`27 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/cms/banners` | public |
| `POST` | `/api/v1/cms/banners` | editor |
| `GET` | `/api/v1/cms/banners/{id}` | public |
| `PUT` | `/api/v1/cms/banners/{id}` | editor |
| `PATCH` | `/api/v1/cms/banners/{id}` | editor |
| `DELETE` | `/api/v1/cms/banners/{id}` | editor |
| `GET` | `/api/v1/cms/faqs` | public |
| `POST` | `/api/v1/cms/faqs` | editor |
| `GET` | `/api/v1/cms/faqs/{id}` | public |
| `PUT` | `/api/v1/cms/faqs/{id}` | editor |
| `PATCH` | `/api/v1/cms/faqs/{id}` | editor |
| `DELETE` | `/api/v1/cms/faqs/{id}` | editor |
| `GET` | `/api/v1/cms/menus` | public |
| `POST` | `/api/v1/cms/menus` | admin |
| `GET` | `/api/v1/cms/menus/{code}/tree` | public |
| `GET` | `/api/v1/cms/menus/{id}` | public |
| `PUT` | `/api/v1/cms/menus/{id}` | admin |
| `PATCH` | `/api/v1/cms/menus/{id}` | admin |
| `DELETE` | `/api/v1/cms/menus/{id}` | admin |
| `GET` | `/api/v1/cms/pages` | public |
| `POST` | `/api/v1/cms/pages` | editor |
| `GET` | `/api/v1/cms/pages/{id}` | public |
| `PUT` | `/api/v1/cms/pages/{id}` | editor |
| `PATCH` | `/api/v1/cms/pages/{id}` | editor |
| `DELETE` | `/api/v1/cms/pages/{id}` | editor |
| `GET` | `/api/v1/cms/settings` | public |
| `PATCH` | `/api/v1/cms/settings` | super_admin |

### Module 03 — Catalog (Products, Variants, Categories, Attributes, Brands)

`33 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/catalog/attributes` | public |
| `POST` | `/api/v1/catalog/attributes` | admin |
| `GET` | `/api/v1/catalog/attributes/{id}` | public |
| `PUT` | `/api/v1/catalog/attributes/{id}` | admin |
| `PATCH` | `/api/v1/catalog/attributes/{id}` | admin |
| `DELETE` | `/api/v1/catalog/attributes/{id}` | admin |
| `GET` | `/api/v1/catalog/brands` | public |
| `POST` | `/api/v1/catalog/brands` | admin |
| `GET` | `/api/v1/catalog/brands/{id}` | public |
| `PUT` | `/api/v1/catalog/brands/{id}` | admin |
| `PATCH` | `/api/v1/catalog/brands/{id}` | admin |
| `DELETE` | `/api/v1/catalog/brands/{id}` | admin |
| `GET` | `/api/v1/catalog/categories` | public |
| `POST` | `/api/v1/catalog/categories` | admin |
| `GET` | `/api/v1/catalog/categories/{id}` | public |
| `PUT` | `/api/v1/catalog/categories/{id}` | admin |
| `PATCH` | `/api/v1/catalog/categories/{id}` | admin |
| `DELETE` | `/api/v1/catalog/categories/{id}` | admin |
| `GET` | `/api/v1/catalog/categories/{id}/tree` | public |
| `GET` | `/api/v1/catalog/products` | public |
| `POST` | `/api/v1/catalog/products` | vendor |
| `GET` | `/api/v1/catalog/products/{id}` | public |
| `PUT` | `/api/v1/catalog/products/{id}` | vendor |
| `PATCH` | `/api/v1/catalog/products/{id}` | vendor |
| `DELETE` | `/api/v1/catalog/products/{id}` | vendor |
| `GET` | `/api/v1/catalog/products/{id}/variants` | public |
| `POST` | `/api/v1/catalog/products/{id}/variants` | vendor |
| `GET` | `/api/v1/catalog/variants` | public |
| `POST` | `/api/v1/catalog/variants` | vendor |
| `GET` | `/api/v1/catalog/variants/{id}` | public |
| `PUT` | `/api/v1/catalog/variants/{id}` | vendor |
| `PATCH` | `/api/v1/catalog/variants/{id}` | vendor |
| `DELETE` | `/api/v1/catalog/variants/{id}` | vendor |

### Module 04 — Inventory & Warehouses

`11 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/inventory/history/{variantId}` | admin |
| `GET` | `/api/v1/inventory/low-stock` | admin |
| `POST` | `/api/v1/inventory/transfer` | admin |
| `GET` | `/api/v1/inventory/warehouses` | admin |
| `POST` | `/api/v1/inventory/warehouses` | admin |
| `GET` | `/api/v1/inventory/warehouses/{id}` | admin |
| `PUT` | `/api/v1/inventory/warehouses/{id}` | admin |
| `PATCH` | `/api/v1/inventory/warehouses/{id}` | admin |
| `DELETE` | `/api/v1/inventory/warehouses/{id}` | admin |
| `GET` | `/api/v1/inventory/{variantId}` | vendor |
| `PATCH` | `/api/v1/inventory/{variantId}` | vendor |

### Module 05 — Shopping Cart

`9 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/cart` | public |
| `DELETE` | `/api/v1/cart` | public |
| `POST` | `/api/v1/cart/apply-coupon` | public |
| `POST` | `/api/v1/cart/items` | public |
| `PATCH` | `/api/v1/cart/items/{itemId}` | public |
| `DELETE` | `/api/v1/cart/items/{itemId}` | public |
| `POST` | `/api/v1/cart/merge` | authenticated |
| `DELETE` | `/api/v1/cart/remove-coupon` | public |
| `GET` | `/api/v1/cart/summary` | public |

### Module 06 — Checkout & Orders

`10 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/checkout/complete` | authenticated |
| `POST` | `/api/v1/checkout/initiate` | authenticated |
| `POST` | `/api/v1/checkout/payment-method` | authenticated |
| `POST` | `/api/v1/checkout/shipping-method` | authenticated |
| `GET` | `/api/v1/orders` | authenticated |
| `GET` | `/api/v1/orders/{id}` | authenticated |
| `POST` | `/api/v1/orders/{id}/cancel` | authenticated |
| `GET` | `/api/v1/orders/{id}/invoice` | authenticated |
| `POST` | `/api/v1/orders/{id}/reorder` | authenticated |
| `GET` | `/api/v1/orders/{id}/track` | authenticated |

### Module 07 — Payments, Gateways & Wallet

`10 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/payments/create-session` | authenticated |
| `GET` | `/api/v1/payments/methods` | public |
| `POST` | `/api/v1/payments/verify` | authenticated |
| `GET` | `/api/v1/payments/wallet/balance` | authenticated |
| `POST` | `/api/v1/payments/wallet/topup` | authenticated |
| `POST` | `/api/v1/payments/webhook/idpay` | public |
| `POST` | `/api/v1/payments/webhook/stripe` | public |
| `POST` | `/api/v1/payments/webhook/zarinpal` | public |
| `GET` | `/api/v1/payments/{id}` | authenticated |
| `POST` | `/api/v1/payments/{id}/refund` | admin |

### Module 08 — Shipping & Delivery

`16 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/shipping/calculate` | public |
| `GET` | `/api/v1/shipping/methods` | public |
| `POST` | `/api/v1/shipping/methods` | admin |
| `GET` | `/api/v1/shipping/methods/{id}` | public |
| `PUT` | `/api/v1/shipping/methods/{id}` | admin |
| `PATCH` | `/api/v1/shipping/methods/{id}` | admin |
| `DELETE` | `/api/v1/shipping/methods/{id}` | admin |
| `POST` | `/api/v1/shipping/shipments` | admin |
| `GET` | `/api/v1/shipping/track/{trackingCode}` | public |
| `POST` | `/api/v1/shipping/webhook/{carrier}` | public |
| `GET` | `/api/v1/shipping/zones` | admin |
| `POST` | `/api/v1/shipping/zones` | admin |
| `GET` | `/api/v1/shipping/zones/{id}` | admin |
| `PUT` | `/api/v1/shipping/zones/{id}` | admin |
| `PATCH` | `/api/v1/shipping/zones/{id}` | admin |
| `DELETE` | `/api/v1/shipping/zones/{id}` | admin |

### Module 09 — Discounts & Coupons

`14 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/discounts/campaigns` | admin |
| `POST` | `/api/v1/discounts/campaigns` | admin |
| `GET` | `/api/v1/discounts/campaigns/{id}` | admin |
| `PUT` | `/api/v1/discounts/campaigns/{id}` | admin |
| `PATCH` | `/api/v1/discounts/campaigns/{id}` | admin |
| `DELETE` | `/api/v1/discounts/campaigns/{id}` | admin |
| `GET` | `/api/v1/discounts/coupons` | admin |
| `POST` | `/api/v1/discounts/coupons` | admin |
| `GET` | `/api/v1/discounts/coupons/{id}` | admin |
| `PUT` | `/api/v1/discounts/coupons/{id}` | admin |
| `PATCH` | `/api/v1/discounts/coupons/{id}` | admin |
| `DELETE` | `/api/v1/discounts/coupons/{id}` | admin |
| `GET` | `/api/v1/discounts/flash-sales` | public |
| `POST` | `/api/v1/discounts/validate` | authenticated |

### Module 10 — Tax

`7 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/tax/calculate` | public |
| `GET` | `/api/v1/tax/rules` | admin |
| `POST` | `/api/v1/tax/rules` | admin |
| `GET` | `/api/v1/tax/rules/{id}` | admin |
| `PUT` | `/api/v1/tax/rules/{id}` | admin |
| `PATCH` | `/api/v1/tax/rules/{id}` | admin |
| `DELETE` | `/api/v1/tax/rules/{id}` | admin |

### Module 11 — Reviews & Ratings

`8 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/reviews` | admin |
| `PATCH` | `/api/v1/admin/reviews/{id}/approve` | admin |
| `GET` | `/api/v1/products/{id}/reviews` | public |
| `POST` | `/api/v1/products/{id}/reviews` | authenticated |
| `PATCH` | `/api/v1/reviews/{id}` | authenticated |
| `DELETE` | `/api/v1/reviews/{id}` | authenticated |
| `POST` | `/api/v1/reviews/{id}/helpful` | authenticated |
| `POST` | `/api/v1/reviews/{id}/report` | authenticated |

### Module 12 — Wishlist & Compare

`6 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/compare` | public |
| `POST` | `/api/v1/compare/{productId}` | public |
| `DELETE` | `/api/v1/compare/{productId}` | public |
| `GET` | `/api/v1/wishlist` | authenticated |
| `POST` | `/api/v1/wishlist/{productId}` | authenticated |
| `DELETE` | `/api/v1/wishlist/{productId}` | authenticated |

### Module 13 — Blog

`14 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/blog/categories` | public |
| `POST` | `/api/v1/blog/categories` | editor |
| `GET` | `/api/v1/blog/categories/{id}` | public |
| `PUT` | `/api/v1/blog/categories/{id}` | editor |
| `PATCH` | `/api/v1/blog/categories/{id}` | editor |
| `DELETE` | `/api/v1/blog/categories/{id}` | editor |
| `GET` | `/api/v1/blog/posts` | public |
| `POST` | `/api/v1/blog/posts` | editor |
| `GET` | `/api/v1/blog/posts/{id}` | public |
| `PUT` | `/api/v1/blog/posts/{id}` | editor |
| `PATCH` | `/api/v1/blog/posts/{id}` | editor |
| `DELETE` | `/api/v1/blog/posts/{id}` | admin |
| `POST` | `/api/v1/blog/posts/{id}/comments` | authenticated |
| `GET` | `/api/v1/blog/rss` | public |

### Module 14 — Vendors (Multi-vendor Marketplace)

`12 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `PATCH` | `/api/v1/admin/vendor-payouts/{id}` | admin |
| `GET` | `/api/v1/admin/vendors` | admin |
| `PATCH` | `/api/v1/admin/vendors/{id}/approve` | admin |
| `PATCH` | `/api/v1/admin/vendors/{id}/suspend` | admin |
| `GET` | `/api/v1/vendors` | public |
| `GET` | `/api/v1/vendors/me` | vendor |
| `PATCH` | `/api/v1/vendors/me` | vendor |
| `GET` | `/api/v1/vendors/me/earnings` | vendor |
| `GET` | `/api/v1/vendors/me/orders` | vendor |
| `POST` | `/api/v1/vendors/me/payout-request` | vendor |
| `GET` | `/api/v1/vendors/me/products` | vendor |
| `POST` | `/api/v1/vendors/register` | authenticated |

### Module 15 — Returns & Refunds

`7 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/returns` | admin |
| `PATCH` | `/api/v1/admin/returns/{id}/approve` | admin |
| `POST` | `/api/v1/admin/returns/{id}/refund` | admin |
| `PATCH` | `/api/v1/admin/returns/{id}/reject` | admin |
| `GET` | `/api/v1/returns` | authenticated |
| `POST` | `/api/v1/returns` | authenticated |
| `GET` | `/api/v1/returns/{id}` | authenticated |

### Module 16 — Media & Upload

`5 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/media` | editor |
| `POST` | `/api/v1/media/bulk-upload` | vendor |
| `POST` | `/api/v1/media/upload` | authenticated |
| `GET` | `/api/v1/media/{id}` | public |
| `DELETE` | `/api/v1/media/{id}` | authenticated |

### Module 17 — Notifications

`7 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/admin/notifications/broadcast` | admin |
| `GET` | `/api/v1/notifications` | authenticated |
| `GET` | `/api/v1/notifications/preferences` | authenticated |
| `PATCH` | `/api/v1/notifications/preferences` | authenticated |
| `PATCH` | `/api/v1/notifications/read-all` | authenticated |
| `POST` | `/api/v1/notifications/subscribe` | authenticated |
| `PATCH` | `/api/v1/notifications/{id}/read` | authenticated |

### Module 18 — Search & Filters

`4 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/search` | public |
| `GET` | `/api/v1/search/filters` | public |
| `GET` | `/api/v1/search/suggestions` | public |
| `POST` | `/api/v1/search/track` | public |

### Module 19 — Admin Dashboard & Reports

`13 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/export/orders` | admin |
| `GET` | `/api/v1/admin/logs/audit` | super_admin |
| `GET` | `/api/v1/admin/logs/errors` | admin |
| `GET` | `/api/v1/admin/orders` | admin |
| `PATCH` | `/api/v1/admin/orders/{id}/status` | admin |
| `GET` | `/api/v1/admin/stats/abandoned-carts` | admin |
| `GET` | `/api/v1/admin/stats/customers` | admin |
| `GET` | `/api/v1/admin/stats/overview` | admin |
| `GET` | `/api/v1/admin/stats/sales` | admin |
| `GET` | `/api/v1/admin/stats/top-products` | admin |
| `GET` | `/api/v1/admin/users` | admin |
| `PATCH` | `/api/v1/admin/users/{id}/ban` | admin |
| `PATCH` | `/api/v1/admin/users/{id}/role` | super_admin |

### Module 20 — Webhooks & Integrations

`8 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/integrations/api-keys` | admin |
| `POST` | `/api/v1/integrations/api-keys` | admin |
| `DELETE` | `/api/v1/integrations/api-keys/{id}` | admin |
| `GET` | `/api/v1/integrations/webhooks` | admin |
| `POST` | `/api/v1/integrations/webhooks` | admin |
| `DELETE` | `/api/v1/integrations/webhooks/{id}` | admin |
| `GET` | `/api/v1/integrations/webhooks/{id}/deliveries` | admin |
| `POST` | `/api/v1/integrations/webhooks/{id}/test` | admin |

### Module 21 — Advanced Reports & Analytics

`45 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/reports` | admin |
| `GET` | `/api/v1/admin/reports/abandoned-carts` | admin |
| `POST` | `/api/v1/admin/reports/abandoned-carts/export` | admin |
| `GET` | `/api/v1/admin/reports/cohort-analysis` | admin |
| `POST` | `/api/v1/admin/reports/cohort-analysis/export` | admin |
| `GET` | `/api/v1/admin/reports/conversion-funnel` | admin |
| `POST` | `/api/v1/admin/reports/conversion-funnel/export` | admin |
| `GET` | `/api/v1/admin/reports/coupon-performance` | admin |
| `POST` | `/api/v1/admin/reports/coupon-performance/export` | admin |
| `POST` | `/api/v1/admin/reports/custom-query` | super_admin |
| `GET` | `/api/v1/admin/reports/customer-ltv` | admin |
| `POST` | `/api/v1/admin/reports/customer-ltv/export` | admin |
| `GET` | `/api/v1/admin/reports/inventory-valuation` | admin |
| `POST` | `/api/v1/admin/reports/inventory-valuation/export` | admin |
| `GET` | `/api/v1/admin/reports/loyalty` | admin |
| `POST` | `/api/v1/admin/reports/loyalty/export` | admin |
| `GET` | `/api/v1/admin/reports/payment-methods-breakdown` | admin |
| `POST` | `/api/v1/admin/reports/payment-methods-breakdown/export` | admin |
| `GET` | `/api/v1/admin/reports/profit-margin` | admin |
| `POST` | `/api/v1/admin/reports/profit-margin/export` | admin |
| `GET` | `/api/v1/admin/reports/returns-refunds` | admin |
| `POST` | `/api/v1/admin/reports/returns-refunds/export` | admin |
| `GET` | `/api/v1/admin/reports/revenue` | admin |
| `POST` | `/api/v1/admin/reports/revenue/export` | admin |
| `GET` | `/api/v1/admin/reports/sales` | admin |
| `POST` | `/api/v1/admin/reports/sales/export` | admin |
| `GET` | `/api/v1/admin/reports/scheduled` | admin |
| `DELETE` | `/api/v1/admin/reports/scheduled/{id}` | admin |
| `GET` | `/api/v1/admin/reports/search-terms` | admin |
| `POST` | `/api/v1/admin/reports/search-terms/export` | admin |
| `GET` | `/api/v1/admin/reports/slow-moving-products` | admin |
| `POST` | `/api/v1/admin/reports/slow-moving-products/export` | admin |
| `GET` | `/api/v1/admin/reports/stock-movements` | admin |
| `POST` | `/api/v1/admin/reports/stock-movements/export` | admin |
| `GET` | `/api/v1/admin/reports/subscriptions` | admin |
| `POST` | `/api/v1/admin/reports/subscriptions/export` | admin |
| `GET` | `/api/v1/admin/reports/tax-summary` | admin |
| `POST` | `/api/v1/admin/reports/tax-summary/export` | admin |
| `GET` | `/api/v1/admin/reports/top-products` | admin |
| `POST` | `/api/v1/admin/reports/top-products/export` | admin |
| `GET` | `/api/v1/admin/reports/traffic-sources` | admin |
| `POST` | `/api/v1/admin/reports/traffic-sources/export` | admin |
| `GET` | `/api/v1/admin/reports/vendor-commissions` | admin |
| `POST` | `/api/v1/admin/reports/vendor-commissions/export` | admin |
| `POST` | `/api/v1/admin/reports/{key}/schedule` | admin |

### Module 22 — Cron Job Management

`9 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/cron/jobs` | super_admin |
| `GET` | `/api/v1/admin/cron/jobs/{id}` | super_admin |
| `GET` | `/api/v1/admin/cron/jobs/{id}/history` | super_admin |
| `GET` | `/api/v1/admin/cron/jobs/{id}/logs` | super_admin |
| `PATCH` | `/api/v1/admin/cron/jobs/{id}/pause` | super_admin |
| `PATCH` | `/api/v1/admin/cron/jobs/{id}/resume` | super_admin |
| `POST` | `/api/v1/admin/cron/jobs/{id}/run` | super_admin |
| `PATCH` | `/api/v1/admin/cron/jobs/{id}/schedule` | super_admin |
| `GET` | `/api/v1/admin/cron/registry` | super_admin |

### Module 23 — Health & Monitoring

`7 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/system/info` | super_admin |
| `GET` | `/api/v1/admin/system/metrics` | super_admin |
| `GET` | `/api/v1/health` | public |
| `GET` | `/api/v1/health/cache` | admin |
| `GET` | `/api/v1/health/db` | admin |
| `GET` | `/api/v1/health/queue` | admin |
| `GET` | `/api/v1/health/ready` | public |

### Module 24 — Backup, Cache & Maintenance

`9 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/admin/system/backup` | super_admin |
| `DELETE` | `/api/v1/admin/system/backup/{id}` | super_admin |
| `POST` | `/api/v1/admin/system/backup/{id}/restore` | super_admin |
| `GET` | `/api/v1/admin/system/backups` | super_admin |
| `POST` | `/api/v1/admin/system/cache/clear` | admin |
| `POST` | `/api/v1/admin/system/cache/clear/{key}` | admin |
| `GET` | `/api/v1/admin/system/maintenance-mode` | admin |
| `PATCH` | `/api/v1/admin/system/maintenance-mode` | super_admin |
| `POST` | `/api/v1/admin/system/optimize` | super_admin |

### Module 25 — SEO & Sitemap

`10 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/seo/broken-links` | admin |
| `GET` | `/api/v1/admin/seo/meta/{entityType}/{id}` | admin |
| `PUT` | `/api/v1/admin/seo/meta/{entityType}/{id}` | admin |
| `GET` | `/api/v1/admin/seo/redirects` | admin |
| `POST` | `/api/v1/admin/seo/redirects` | admin |
| `GET` | `/api/v1/admin/seo/redirects/{id}` | admin |
| `PUT` | `/api/v1/admin/seo/redirects/{id}` | admin |
| `PATCH` | `/api/v1/admin/seo/redirects/{id}` | admin |
| `DELETE` | `/api/v1/admin/seo/redirects/{id}` | admin |
| `POST` | `/api/v1/admin/seo/sitemap/regenerate` | admin |

### Module 26 — Currency & Localization

`7 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `PATCH` | `/api/v1/admin/localization/currencies/{code}` | admin |
| `PUT` | `/api/v1/admin/localization/translations/{locale}` | admin |
| `POST` | `/api/v1/localization/convert` | public |
| `GET` | `/api/v1/localization/currencies` | public |
| `GET` | `/api/v1/localization/exchange-rates` | public |
| `GET` | `/api/v1/localization/languages` | public |
| `GET` | `/api/v1/localization/translations/{locale}` | public |

### Module 27 — Loyalty, Gift Cards & Referral

`10 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/loyalty/accounts` | admin |
| `GET` | `/api/v1/admin/referral/stats` | admin |
| `GET` | `/api/v1/gift-cards` | authenticated |
| `POST` | `/api/v1/gift-cards/purchase` | authenticated |
| `POST` | `/api/v1/gift-cards/redeem` | authenticated |
| `GET` | `/api/v1/loyalty/me` | authenticated |
| `POST` | `/api/v1/loyalty/redeem` | authenticated |
| `GET` | `/api/v1/loyalty/rules` | public |
| `POST` | `/api/v1/referral/apply` | public |
| `GET` | `/api/v1/referral/me` | authenticated |

### Module 28 — Subscriptions & Recurring Billing

`14 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/admin/subscriptions` | admin |
| `POST` | `/api/v1/admin/subscriptions/run-billing` | super_admin |
| `GET` | `/api/v1/subscription-plans` | public |
| `POST` | `/api/v1/subscription-plans` | admin |
| `GET` | `/api/v1/subscription-plans/{id}` | public |
| `PUT` | `/api/v1/subscription-plans/{id}` | admin |
| `PATCH` | `/api/v1/subscription-plans/{id}` | admin |
| `DELETE` | `/api/v1/subscription-plans/{id}` | admin |
| `GET` | `/api/v1/subscriptions` | authenticated |
| `POST` | `/api/v1/subscriptions` | authenticated |
| `GET` | `/api/v1/subscriptions/{id}` | authenticated |
| `POST` | `/api/v1/subscriptions/{id}/cancel` | authenticated |
| `PATCH` | `/api/v1/subscriptions/{id}/plan` | authenticated |
| `POST` | `/api/v1/subscriptions/{id}/resume` | authenticated |

### Module 29 — Import / Export & Bulk Operations

`9 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/admin/bulk/inventory` | admin |
| `POST` | `/api/v1/admin/bulk/orders` | admin |
| `POST` | `/api/v1/admin/bulk/products` | admin |
| `GET` | `/api/v1/admin/export/{type}` | admin |
| `GET` | `/api/v1/admin/import-jobs` | admin |
| `GET` | `/api/v1/admin/import-jobs/{id}` | admin |
| `GET` | `/api/v1/admin/import/types` | admin |
| `POST` | `/api/v1/admin/import/{type}` | admin |
| `GET` | `/api/v1/admin/import/{type}/template` | admin |

### Module 30 — Queue Management

`10 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `POST` | `/api/v1/admin/queue-jobs` | super_admin |
| `GET` | `/api/v1/admin/queue-jobs/{id}` | admin |
| `DELETE` | `/api/v1/admin/queue-jobs/{id}` | admin |
| `POST` | `/api/v1/admin/queue-jobs/{id}/retry` | admin |
| `GET` | `/api/v1/admin/queues` | admin |
| `GET` | `/api/v1/admin/queues/failed` | admin |
| `GET` | `/api/v1/admin/queues/throughput` | admin |
| `GET` | `/api/v1/admin/queues/{name}/jobs` | admin |
| `POST` | `/api/v1/admin/queues/{name}/purge` | super_admin |
| `POST` | `/api/v1/admin/queues/{name}/retry-failed` | admin |

### Module 31 — Feature Flags & A/B Testing

`18 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/ab-tests/{key}/assign` | public |
| `POST` | `/api/v1/ab-tests/{key}/convert` | public |
| `GET` | `/api/v1/admin/ab-tests` | admin |
| `POST` | `/api/v1/admin/ab-tests` | admin |
| `GET` | `/api/v1/admin/ab-tests/{id}` | admin |
| `PUT` | `/api/v1/admin/ab-tests/{id}` | admin |
| `PATCH` | `/api/v1/admin/ab-tests/{id}` | admin |
| `DELETE` | `/api/v1/admin/ab-tests/{id}` | admin |
| `GET` | `/api/v1/admin/ab-tests/{key}/results` | admin |
| `GET` | `/api/v1/admin/feature-flags` | admin |
| `POST` | `/api/v1/admin/feature-flags` | admin |
| `GET` | `/api/v1/admin/feature-flags/{id}` | admin |
| `PUT` | `/api/v1/admin/feature-flags/{id}` | admin |
| `PATCH` | `/api/v1/admin/feature-flags/{id}` | admin |
| `DELETE` | `/api/v1/admin/feature-flags/{id}` | admin |
| `POST` | `/api/v1/admin/feature-flags/{key}/toggle` | admin |
| `GET` | `/api/v1/feature-flags` | public |
| `GET` | `/api/v1/feature-flags/{key}` | public |

### Meta — Introspection

`2 endpoints`

| Method | Endpoint | Access |
|---|---|---|
| `GET` | `/api/v1/_errors` | public |
| `GET` | `/api/v1/_routes` | public |
---

## 7. Worked Examples

### 7.1 Browse the catalog (public)

```http
GET /api/v1/catalog/products?page=1&limit=12&sort=price_asc&category=laptops&minPrice=50000000
```

```json
{
  "success": true,
  "statusCode": 200,
  "message": "OK",
  "data": [
    {
      "id": 1,
      "slug": "macbook-air-m3",
      "name": "مک‌بوک ایر M3 ۱۳ اینچ",
      "name_en": "MacBook Air M3 13\"",
      "price": 920000000,
      "compare_price": 1050000000,
      "currency": "IRR",
      "rating_avg": 5,
      "thumbnail": "/static/img/products/macbook-air-m3.svg",
      "status": "published"
    }
  ],
  "meta": { "pagination": { "page": 1, "limit": 12, "totalItems": 4, "totalPages": 1 } },
  "timestamp": "2026-10-04T08:11:31Z"
}
```

### 7.2 Add to cart (guest or customer)

The cart is bound to the `ecms_cart` cookie for guests and merged into the user
cart on login.

```http
POST /api/v1/cart/items
Content-Type: application/json

{ "productId": 1, "variantId": 1, "quantity": 2 }
```

The HTML layer posts the same intent as a form to `POST /cart/add` and receives
the new badge count as a bare HTML fragment for HTMX to swap.

### 7.3 Price the cart

```http
GET /api/v1/cart?shippingMethodId=1
```

```json
{
  "data": {
    "cartId": 12,
    "currency": "IRR",
    "items": [ { "itemId": 31, "name": "…", "quantity": 2, "unitPrice": 920000000, "lineTotal": 1840000000, "stock": 14, "outOfStock": false } ],
    "itemCount": 2,
    "subtotal": 1840000000,
    "discount": 184000000,
    "tax": 149040000,
    "shipping": 560000,
    "grandTotal": 1805600000,
    "coupon": "WELCOME10",
    "couponMessage": "10% off your first order",
    "freeShipping": false,
    "totalWeightG": 2440
  }
}
```

Pricing pipeline: `subtotal` → `ValidateCoupon` → `CalculateTax(subtotal − discount)`
→ `ShippingCost` (zeroed by a `free_shipping` coupon or the `free_shipping_over`
threshold) → `grandTotal = subtotal − discount + tax + shipping`, floored at 0.

### 7.4 Complete checkout

```http
POST /api/v1/checkout/complete
Authorization: Bearer <jwt>
Idempotency-Key: 6f1b2c0e-9c1e-4b4a-9c6f-1d9f0a4a7b22
Content-Type: application/json

{
  "shippingMethodId": 1,
  "paymentMethod": "zarinpal",
  "couponCode": "WELCOME10",
  "address": {
    "recipient": "علی رضایی",
    "phone": "09120000000",
    "country": "IR",
    "province": "تهران",
    "city": "تهران",
    "postal_code": "1234567890",
    "line1": "خیابان ولیعصر، پلاک ۱"
  }
}
```

**201 Created**

```json
{
  "success": true,
  "statusCode": 201,
  "message": "Order placed",
  "data": {
    "order": { "id": 41, "number": "ORD-261004-279B", "status": "pending", "payment_status": "unpaid", "grand_total": 1805600000 },
    "payment": { "reference": "PAY-7D55365DDA821F63", "gateway": "zarinpal", "status": "pending", "sandbox": true, "redirectUrl": "/api/v1/payments/sandbox/PAY-7D55365DDA821F63" }
  }
}
```

Server-side the handler re-validates stock, inserts `orders` + `order_items`,
reserves stock, bumps `sold_count`, writes an `order_events` row, increments the
coupon counter, marks the cart `converted`, opens a payment session, awards
loyalty points, notifies the customer and dispatches the `order.created` webhook.

### 7.5 Settle the sandbox payment

No real money moves. `wallet` settles inline; `zarinpal` and `stripe` return a
pending reference you resolve explicitly:

```http
POST /api/v1/payments/verify
Content-Type: application/json

{ "reference": "PAY-7D55365DDA821F63", "outcome": "success" }
```

`outcome` is `success` or `fail`. On success the order flips to `paid`, stock is
committed, the invoice is queued and `order.paid` fires. The storefront exposes
the same thing as two buttons on the order confirmation page
(`POST /pay/simulate`).

### 7.6 Webhook delivery contract

```http
POST https://your-endpoint.example.com/hooks
X-Event: order.created
X-Signature: 9a2f...   (HMAC-SHA256 hex of the raw body, keyed with the subscriber secret)
User-Agent: Avaluna-Webhook/1.0
Content-Type: application/json

{ "event": "order.created", "data": { }, "at": "2026-10-04T08:11:31Z" }
```

Verify by recomputing `HMAC_SHA256(secret, rawBody)` and comparing in constant
time. A subscriber's `events` field is `*` or a comma-separated substring match.
Failures are retried by the `webhook-retry-failed` job (5 attempts, exponential
backoff).

---

## 8. Scheduled Jobs (24)

Managed through Module 22 (`/api/v1/admin/cron/*`) and the admin UI at
`/admin/cron`, where every job can be run on demand, paused or resumed.

| # | Job | Schedule | Retry | Purpose |
|---|---|---|---|---|
| 1 | `abandoned-cart-reminder` | `0 * * * *` | 3× backoff | Nudge carts idle > 24 h |
| 2 | `order-auto-cancel-unpaid` | `*/15 * * * *` | 2× | Cancel unpaid orders past the window |
| 3 | `inventory-low-stock-alert` | `0 8 * * *` | 1× | Alert on variants at/below reorder level |
| 4 | `currency-exchange-rate-sync` | `0 */6 * * *` | 5× backoff | Refresh FX rates |
| 5 | `flash-sale-price-activator` | `* * * * *` | 3× | Activate/expire flash sales |
| 6 | `subscription-renewal-billing` | `0 2 * * *` | 5× | Charge due subscriptions |
| 7 | `license-expiry-check` | `0 3 * * *` | 2× | Warn before licence expiry |
| 8 | `coupon-expiry-cleanup` | `0 0 * * *` | 1× | Deactivate expired/exhausted coupons |
| 9 | `vendor-payout-processing` | `0 4 * * 1` | 3× | Weekly vendor payouts |
| 10 | `tax-report-generation` | `0 5 1 * *` | 2× | Monthly tax summary |
| 11 | `database-backup` | `0 1 * * *` | 3× + alert | Nightly snapshot with rotation |
| 12 | `cache-warmup` | `*/30 * * * *` | 1× | Pre-compute hot caches |
| 13 | `sitemap-regeneration` | `0 0 * * *` | 2× | Rebuild `sitemap.xml` |
| 14 | `search-index-rebuild` | `0 0 * * *` | 2× | Rebuild the FTS5 index |
| 15 | `analytics-aggregation` | `0 * * * *` | 2× | Roll hourly analytics up |
| 16 | `review-reminder-email` | `0 9 * * *` | 1× | Ask for reviews after delivery |
| 17 | `session-cleanup` | `0 0 * * *` | 1× | Purge expired tokens & idempotency keys |
| 18 | `fraud-detection-scan` | `0 * * * *` | 3× | Flag payment velocity / login bursts |
| 19 | `gift-card-expiry-check` | `0 6 * * *` | 1× | Expire stale gift cards |
| 20 | `loyalty-points-expiry` | `0 0 1 * *` | 1× | Expire aged loyalty points |
| 21 | `webhook-retry-failed` | `*/5 * * * *` | 5× backoff | Re-deliver failed webhooks |
| 22 | `invoice-pdf-retry` | `*/10 * * * *` | 3× | Regenerate missing invoices |
| 23 | `log-rotation-cleanup` | `0 0 * * 0` | 1× | Rotate audit/error/analytics logs |
| 24 | `health-check-ping` | `*/5 * * * *` | — | Probe DB, queue and disk |

The scheduler is a dependency-free 5-field cron implementation with per-job
locking (`cron_jobs.running`), run history (`cron_runs`), success/failure
counters and rolling average duration.

---

## 9. Background Queue

Six durable handlers, backed by the `queue_jobs` table, four workers by default:

`webhook.deliver` · `email.send` · `invoice.render` · `report.generate` ·
`search.reindex` · `media.optimize`

Statuses: `waiting` → `active` → `completed` | `failed` | `delayed`
(exponential backoff `2^attempt × 10 s`). Panics inside a handler are recovered
and recorded as a failure — a bad job can never take a worker down. Managed at
`/api/v1/admin/queues/*` and `/admin/queue`.

---

## 10. Security Checklist

| Control | Implementation |
|---|---|
| Password storage | Argon2id + per-user salt + server pepper |
| Session tokens | 15-minute JWT, rotating hashed refresh cookie with family revocation |
| 2FA | TOTP (RFC 6238), mandatory for staff and vendors once enabled |
| Transport headers | CSP, HSTS, `X-Content-Type-Options`, `Referrer-Policy`, `Permissions-Policy` |
| CORS | Explicit allow-list from the `cors_origins` setting; no wildcard with credentials |
| CSRF | Double-submit cookie on every HTML form |
| SQL injection | 100 % parameterised queries — no string-built SQL with user input |
| XSS | `html/template` contextual auto-escaping; raw HTML only via an explicit, reviewed `safe` helper |
| Rate limiting | Six tiers, per IP + scope |
| Idempotency | Required key on financial writes, 24-hour replay |
| Mass assignment | Explicit column allow-lists in the generic resource layer |
| Upload safety | 10 MiB cap, MIME sniffing, randomised names, stored outside the web root |
| Audit trail | `audit_log` records actor, role, action, entity and IP |
| Error leakage | Internal errors are logged with a request id; clients receive a generic message |
| Secrets | Read from the environment; never logged or echoed |
| Soft delete | Destructive operations are reversible by default |

---

## 11. Demo Credentials & Seed Data

| Role | Email | Password |
|---|---|---|
| `super_admin` | `admin@yourstore.com` | `Admin@12345` |
| `vendor` | `vendor1@yourstore.com` | `Vendor@12345` |
| `customer` | `ali@example.com` | `Customer@12345` |

Seeded on first boot: 24 products with variants and inventory, 8 categories,
6 brands, 2 vendors, 40 orders across every status, coupons (`WELCOME10`,
`FREESHIP`, `SUMMER25`), 8 blog posts with comments, reviews, CMS pages, FAQs,
menus, 2 currencies, tax rules, shipping zones and methods, payment methods,
loyalty accounts, gift cards, subscriptions, notifications, webhooks, feature
flags, analytics events and the 24 cron job definitions.

---

## 12. Running It

```bash
make deps      # download modules
make css       # build the Tailwind stylesheet
make run       # migrate + seed + serve on :3000
make check     # fmt + vet + staticcheck + tests
```

| Variable | Default | Purpose |
|---|---|---|
| `PORT` | `3000` | Listen port |
| `HOST` | `0.0.0.0` | Bind address |
| `APP_ENV` | `development` | `production` switches to JSON logs + HSTS |
| `DATABASE_PATH` | `data/store.db` | SQLite file |
| `JWT_SECRET` | dev default | **Set this in production** |
| `ARGON_PEPPER` | dev default | **Set this in production** |
| `DEFAULT_LOCALE` | `fa` | `fa` or `en` |
| `DEFAULT_CURRENCY` | `IRR` | Display currency |
| `ENABLE_CRON` | `true` | Start the scheduler |
| `ENABLE_QUEUE` | `true` | Start the workers |
| `QUEUE_CONCURRENCY` | `4` | Worker count |
| `AUTO_SEED` | `true` | Seed demo data on an empty database |
