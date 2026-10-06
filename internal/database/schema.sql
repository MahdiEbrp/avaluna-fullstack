
CREATE TABLE IF NOT EXISTS users (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid              TEXT    NOT NULL UNIQUE,
  email             TEXT    NOT NULL UNIQUE,
  phone             TEXT,
  password_hash     TEXT    NOT NULL,
  first_name        TEXT    NOT NULL DEFAULT '',
  last_name         TEXT    NOT NULL DEFAULT '',
  role              TEXT    NOT NULL DEFAULT 'customer',  -- guest|customer|vendor|editor|support|admin|super_admin
  status            TEXT    NOT NULL DEFAULT 'active',    -- active|banned|pending
  email_verified_at TEXT,
  twofa_secret      TEXT,
  twofa_enabled     INTEGER NOT NULL DEFAULT 0,
  locale            TEXT    NOT NULL DEFAULT 'fa',
  currency          TEXT    NOT NULL DEFAULT 'IRR',
  avatar_media_id   INTEGER,
  referral_code     TEXT    UNIQUE,
  referred_by       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  last_login_at     TEXT,
  failed_logins     INTEGER NOT NULL DEFAULT 0,
  locked_until      TEXT,
  created_at        TEXT    NOT NULL DEFAULT (datetime('now')),
  updated_at        TEXT    NOT NULL DEFAULT (datetime('now')),
  deleted_at        TEXT
);
CREATE INDEX IF NOT EXISTS idx_users_role   ON users(role);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

CREATE TABLE IF NOT EXISTS refresh_tokens (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT    NOT NULL UNIQUE,
  family      TEXT    NOT NULL,
  user_agent  TEXT    NOT NULL DEFAULT '',
  ip          TEXT    NOT NULL DEFAULT '',
  revoked_at  TEXT,
  reused      INTEGER NOT NULL DEFAULT 0,
  expires_at  TEXT    NOT NULL,
  created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_rt_user ON refresh_tokens(user_id);

CREATE TABLE IF NOT EXISTS auth_tokens (          -- email verify / password reset / 2fa challenge
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  purpose    TEXT    NOT NULL,
  token_hash TEXT    NOT NULL,
  used_at    TEXT,
  expires_at TEXT    NOT NULL,
  created_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS addresses (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label       TEXT    NOT NULL DEFAULT 'home',
  recipient   TEXT    NOT NULL,
  phone       TEXT    NOT NULL,
  country     TEXT    NOT NULL DEFAULT 'IR',
  province    TEXT    NOT NULL DEFAULT '',
  city        TEXT    NOT NULL DEFAULT '',
  postal_code TEXT    NOT NULL DEFAULT '',
  line1       TEXT    NOT NULL,
  line2       TEXT    NOT NULL DEFAULT '',
  is_default  INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT    NOT NULL DEFAULT (datetime('now')),
  deleted_at  TEXT
);

CREATE TABLE IF NOT EXISTS pages (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  slug        TEXT    NOT NULL UNIQUE,
  title       TEXT    NOT NULL,
  title_en    TEXT    NOT NULL DEFAULT '',
  body        TEXT    NOT NULL DEFAULT '',
  body_en     TEXT    NOT NULL DEFAULT '',
  status      TEXT    NOT NULL DEFAULT 'published',
  template    TEXT    NOT NULL DEFAULT 'default',
  author_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at  TEXT    NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT    NOT NULL DEFAULT (datetime('now')),
  deleted_at  TEXT
);

CREATE TABLE IF NOT EXISTS menus (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  code       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  items_json TEXT NOT NULL DEFAULT '[]',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  scope      TEXT NOT NULL DEFAULT 'public',    -- public|private
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS banners (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT NOT NULL,
  title_en    TEXT NOT NULL DEFAULT '',
  subtitle    TEXT NOT NULL DEFAULT '',
  subtitle_en TEXT NOT NULL DEFAULT '',
  image_url   TEXT NOT NULL DEFAULT '',
  link        TEXT NOT NULL DEFAULT '',
  position    TEXT NOT NULL DEFAULT 'hero',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  active      INTEGER NOT NULL DEFAULT 1,
  starts_at   TEXT, ends_at TEXT,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS faqs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  question    TEXT NOT NULL, question_en TEXT NOT NULL DEFAULT '',
  answer      TEXT NOT NULL, answer_en   TEXT NOT NULL DEFAULT '',
  category    TEXT NOT NULL DEFAULT 'general',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  active      INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS categories (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  parent_id   INTEGER REFERENCES categories(id) ON DELETE CASCADE,
  slug        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  image_url   TEXT NOT NULL DEFAULT '',
  icon        TEXT NOT NULL DEFAULT '',
  sort_order  INTEGER NOT NULL DEFAULT 0,
  active      INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
  deleted_at  TEXT
);

CREATE TABLE IF NOT EXISTS brands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  slug       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  logo_url   TEXT NOT NULL DEFAULT '',
  country    TEXT NOT NULL DEFAULT '',
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS attributes (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  code        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  type        TEXT NOT NULL DEFAULT 'select',   -- select|color|text|number|bool
  values_json TEXT NOT NULL DEFAULT '[]',
  filterable  INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS products (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  slug            TEXT NOT NULL UNIQUE,
  sku             TEXT NOT NULL UNIQUE,
  name            TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  short_desc      TEXT NOT NULL DEFAULT '', short_desc_en TEXT NOT NULL DEFAULT '',
  description     TEXT NOT NULL DEFAULT '', description_en TEXT NOT NULL DEFAULT '',
  category_id     INTEGER REFERENCES categories(id) ON DELETE SET NULL,
  brand_id        INTEGER REFERENCES brands(id) ON DELETE SET NULL,
  vendor_id       INTEGER,
  price           INTEGER NOT NULL DEFAULT 0,       -- minor units
  compare_price   INTEGER NOT NULL DEFAULT 0,
  cost_price      INTEGER NOT NULL DEFAULT 0,
  currency        TEXT NOT NULL DEFAULT 'IRR',
  tax_class       TEXT NOT NULL DEFAULT 'standard',
  weight_g        INTEGER NOT NULL DEFAULT 0,
  status          TEXT NOT NULL DEFAULT 'published', -- draft|published|archived
  featured        INTEGER NOT NULL DEFAULT 0,
  thumbnail       TEXT NOT NULL DEFAULT '',
  gallery_json    TEXT NOT NULL DEFAULT '[]',
  attrs_json      TEXT NOT NULL DEFAULT '{}',
  rating_avg      REAL NOT NULL DEFAULT 0,
  rating_count    INTEGER NOT NULL DEFAULT 0,
  sold_count      INTEGER NOT NULL DEFAULT 0,
  view_count      INTEGER NOT NULL DEFAULT 0,
  published_at    TEXT,
  created_at      TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
  deleted_at      TEXT
);
CREATE INDEX IF NOT EXISTS idx_products_cat    ON products(category_id);
CREATE INDEX IF NOT EXISTS idx_products_brand  ON products(brand_id);
CREATE INDEX IF NOT EXISTS idx_products_status ON products(status);
CREATE INDEX IF NOT EXISTS idx_products_price  ON products(price);

CREATE TABLE IF NOT EXISTS product_variants (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  sku         TEXT NOT NULL UNIQUE,
  title       TEXT NOT NULL DEFAULT '',
  options_json TEXT NOT NULL DEFAULT '{}',
  price       INTEGER NOT NULL DEFAULT 0,
  compare_price INTEGER NOT NULL DEFAULT 0,
  barcode     TEXT NOT NULL DEFAULT '',
  weight_g    INTEGER NOT NULL DEFAULT 0,
  active      INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_variants_product ON product_variants(product_id);

CREATE TABLE IF NOT EXISTS warehouses (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  code       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  city       TEXT NOT NULL DEFAULT '',
  address    TEXT NOT NULL DEFAULT '',
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS inventory (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  variant_id    INTEGER NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
  warehouse_id  INTEGER NOT NULL REFERENCES warehouses(id) ON DELETE CASCADE,
  quantity      INTEGER NOT NULL DEFAULT 0,
  reserved      INTEGER NOT NULL DEFAULT 0,
  reorder_level INTEGER NOT NULL DEFAULT 5,
  updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(variant_id, warehouse_id)
);

CREATE TABLE IF NOT EXISTS inventory_history (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  variant_id   INTEGER NOT NULL,
  warehouse_id INTEGER NOT NULL,
  delta        INTEGER NOT NULL,
  reason       TEXT NOT NULL,
  reference    TEXT NOT NULL DEFAULT '',
  actor_id     INTEGER,
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS carts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  token       TEXT NOT NULL UNIQUE,
  user_id     INTEGER REFERENCES users(id) ON DELETE CASCADE,
  currency    TEXT NOT NULL DEFAULT 'IRR',
  coupon_code TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'active',  -- active|converted|abandoned
  notes       TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cart_items (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  cart_id    INTEGER NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
  product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  variant_id INTEGER REFERENCES product_variants(id) ON DELETE SET NULL,
  quantity   INTEGER NOT NULL DEFAULT 1,
  unit_price INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_cart_items_cart ON cart_items(cart_id);

CREATE TABLE IF NOT EXISTS orders (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  number            TEXT NOT NULL UNIQUE,
  user_id           INTEGER REFERENCES users(id) ON DELETE SET NULL,
  email             TEXT NOT NULL DEFAULT '',
  status            TEXT NOT NULL DEFAULT 'pending',  -- pending|paid|processing|shipped|delivered|cancelled|refunded
  payment_status    TEXT NOT NULL DEFAULT 'unpaid',   -- unpaid|paid|partially_refunded|refunded|failed
  fulfillment_status TEXT NOT NULL DEFAULT 'unfulfilled',
  currency          TEXT NOT NULL DEFAULT 'IRR',
  subtotal          INTEGER NOT NULL DEFAULT 0,
  discount_total    INTEGER NOT NULL DEFAULT 0,
  tax_total         INTEGER NOT NULL DEFAULT 0,
  shipping_total    INTEGER NOT NULL DEFAULT 0,
  grand_total       INTEGER NOT NULL DEFAULT 0,
  coupon_code       TEXT NOT NULL DEFAULT '',
  shipping_method_id INTEGER,
  payment_method    TEXT NOT NULL DEFAULT '',
  address_json      TEXT NOT NULL DEFAULT '{}',
  tracking_code     TEXT NOT NULL DEFAULT '',
  notes             TEXT NOT NULL DEFAULT '',
  placed_at         TEXT,
  paid_at           TEXT,
  shipped_at        TEXT,
  delivered_at      TEXT,
  cancelled_at      TEXT,
  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_orders_user   ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);

CREATE TABLE IF NOT EXISTS order_items (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id     INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id   INTEGER,
  variant_id   INTEGER,
  vendor_id    INTEGER,
  name         TEXT NOT NULL,
  sku          TEXT NOT NULL DEFAULT '',
  quantity     INTEGER NOT NULL DEFAULT 1,
  unit_price   INTEGER NOT NULL DEFAULT 0,
  cost_price   INTEGER NOT NULL DEFAULT 0,
  discount     INTEGER NOT NULL DEFAULT 0,
  tax          INTEGER NOT NULL DEFAULT 0,
  total        INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items(order_id);

CREATE TABLE IF NOT EXISTS order_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id   INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  status     TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  actor_id   INTEGER,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  key           TEXT PRIMARY KEY,
  user_id       INTEGER,
  endpoint      TEXT NOT NULL,
  response_json TEXT NOT NULL DEFAULT '',
  status_code   INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS payment_methods (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  code       TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  provider   TEXT NOT NULL DEFAULT 'sandbox',
  fee_fixed  INTEGER NOT NULL DEFAULT 0,
  fee_percent REAL NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  sort_order INTEGER NOT NULL DEFAULT 0,
  config_json TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS payments (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  reference     TEXT NOT NULL UNIQUE,
  order_id      INTEGER REFERENCES orders(id) ON DELETE SET NULL,
  user_id       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  gateway       TEXT NOT NULL,
  amount        INTEGER NOT NULL,
  currency      TEXT NOT NULL DEFAULT 'IRR',
  status        TEXT NOT NULL DEFAULT 'pending', -- pending|succeeded|failed|refunded
  authority     TEXT NOT NULL DEFAULT '',
  card_mask     TEXT NOT NULL DEFAULT '',
  refunded      INTEGER NOT NULL DEFAULT 0,
  meta_json     TEXT NOT NULL DEFAULT '{}',
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS wallets (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  balance    INTEGER NOT NULL DEFAULT 0,
  currency   TEXT NOT NULL DEFAULT 'IRR',
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount     INTEGER NOT NULL,
  type       TEXT NOT NULL,       -- topup|purchase|refund|payout|adjust
  balance_after INTEGER NOT NULL DEFAULT 0,
  reference  TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS shipping_zones (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  countries  TEXT NOT NULL DEFAULT 'IR',
  provinces  TEXT NOT NULL DEFAULT '',
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS shipping_methods (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  zone_id     INTEGER REFERENCES shipping_zones(id) ON DELETE CASCADE,
  code        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  carrier     TEXT NOT NULL DEFAULT 'post',
  base_cost   INTEGER NOT NULL DEFAULT 0,
  per_kg_cost INTEGER NOT NULL DEFAULT 0,
  free_over   INTEGER NOT NULL DEFAULT 0,
  eta_days    INTEGER NOT NULL DEFAULT 3,
  active      INTEGER NOT NULL DEFAULT 1,
  sort_order  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS shipments (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id      INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  carrier       TEXT NOT NULL DEFAULT 'post',
  tracking_code TEXT NOT NULL UNIQUE,
  status        TEXT NOT NULL DEFAULT 'label_created',
  events_json   TEXT NOT NULL DEFAULT '[]',
  shipped_at    TEXT, delivered_at TEXT,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS coupons (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  code           TEXT NOT NULL UNIQUE,
  type           TEXT NOT NULL DEFAULT 'percent',  -- percent|fixed|free_shipping
  value          INTEGER NOT NULL DEFAULT 0,
  min_subtotal   INTEGER NOT NULL DEFAULT 0,
  max_discount   INTEGER NOT NULL DEFAULT 0,
  usage_limit    INTEGER NOT NULL DEFAULT 0,
  used_count     INTEGER NOT NULL DEFAULT 0,
  per_user_limit INTEGER NOT NULL DEFAULT 1,
  campaign_id    INTEGER,
  starts_at      TEXT, expires_at TEXT,
  active         INTEGER NOT NULL DEFAULT 1,
  created_at     TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS coupon_redemptions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  coupon_id  INTEGER NOT NULL REFERENCES coupons(id) ON DELETE CASCADE,
  user_id    INTEGER,
  order_id   INTEGER,
  amount     INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS campaigns (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  type       TEXT NOT NULL DEFAULT 'flash_sale',
  discount_percent INTEGER NOT NULL DEFAULT 0,
  product_ids TEXT NOT NULL DEFAULT '[]',
  starts_at  TEXT, ends_at TEXT,
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS tax_rules (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  country    TEXT NOT NULL DEFAULT 'IR',
  province   TEXT NOT NULL DEFAULT '',
  tax_class  TEXT NOT NULL DEFAULT 'standard',
  rate       REAL NOT NULL DEFAULT 9.0,
  compound   INTEGER NOT NULL DEFAULT 0,
  priority   INTEGER NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS reviews (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  product_id  INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  order_id    INTEGER,
  rating      INTEGER NOT NULL DEFAULT 5,
  title       TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '',
  verified    INTEGER NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'pending',  -- pending|approved|rejected
  helpful     INTEGER NOT NULL DEFAULT 0,
  reports     INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_reviews_product ON reviews(product_id);

CREATE TABLE IF NOT EXISTS wishlist_items (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(user_id, product_id)
);

CREATE TABLE IF NOT EXISTS compare_items (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  token      TEXT NOT NULL,
  product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(token, product_id)
);

CREATE TABLE IF NOT EXISTS blog_categories (
  id   INTEGER PRIMARY KEY AUTOINCREMENT,
  slug TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS blog_posts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  slug        TEXT NOT NULL UNIQUE,
  title       TEXT NOT NULL, title_en TEXT NOT NULL DEFAULT '',
  excerpt     TEXT NOT NULL DEFAULT '', excerpt_en TEXT NOT NULL DEFAULT '',
  body        TEXT NOT NULL DEFAULT '', body_en TEXT NOT NULL DEFAULT '',
  cover_url   TEXT NOT NULL DEFAULT '',
  category_id INTEGER REFERENCES blog_categories(id) ON DELETE SET NULL,
  author_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
  tags        TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'published',
  views       INTEGER NOT NULL DEFAULT 0,
  published_at TEXT,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
  deleted_at  TEXT
);

CREATE TABLE IF NOT EXISTS blog_comments (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  post_id    INTEGER NOT NULL REFERENCES blog_posts(id) ON DELETE CASCADE,
  user_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  body       TEXT NOT NULL,
  status     TEXT NOT NULL DEFAULT 'pending',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS vendors (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug          TEXT NOT NULL UNIQUE,
  shop_name     TEXT NOT NULL, shop_name_en TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  logo_url      TEXT NOT NULL DEFAULT '',
  commission_rate REAL NOT NULL DEFAULT 10.0,
  balance       INTEGER NOT NULL DEFAULT 0,
  status        TEXT NOT NULL DEFAULT 'pending',   -- pending|approved|suspended
  rating        REAL NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS vendor_payouts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  vendor_id  INTEGER NOT NULL REFERENCES vendors(id) ON DELETE CASCADE,
  amount     INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT 'requested', -- requested|approved|paid|rejected
  iban       TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  paid_at    TEXT
);

CREATE TABLE IF NOT EXISTS returns (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  number      TEXT NOT NULL UNIQUE,
  order_id    INTEGER NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  items_json  TEXT NOT NULL DEFAULT '[]',
  reason      TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'requested', -- requested|approved|rejected|refunded
  refund_amount INTEGER NOT NULL DEFAULT 0,
  admin_note  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS media (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  uuid        TEXT NOT NULL UNIQUE,
  filename    TEXT NOT NULL,
  mime        TEXT NOT NULL DEFAULT '',
  size_bytes  INTEGER NOT NULL DEFAULT 0,
  path        TEXT NOT NULL,
  url         TEXT NOT NULL,
  alt         TEXT NOT NULL DEFAULT '',
  owner_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  scanned     INTEGER NOT NULL DEFAULT 1,
  optimized   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS notifications (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type       TEXT NOT NULL DEFAULT 'system',
  title      TEXT NOT NULL,
  body       TEXT NOT NULL DEFAULT '',
  link       TEXT NOT NULL DEFAULT '',
  read_at    TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_notif_user ON notifications(user_id);

CREATE TABLE IF NOT EXISTS notification_preferences (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  email      INTEGER NOT NULL DEFAULT 1,
  sms        INTEGER NOT NULL DEFAULT 0,
  push       INTEGER NOT NULL DEFAULT 1,
  marketing  INTEGER NOT NULL DEFAULT 1,
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS push_subscriptions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  endpoint   TEXT NOT NULL,
  keys_json  TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS search_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  query      TEXT NOT NULL,
  results    INTEGER NOT NULL DEFAULT 0,
  user_id    INTEGER,
  session    TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS audit_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_id   INTEGER,
  actor_role TEXT NOT NULL DEFAULT '',
  action     TEXT NOT NULL,
  entity     TEXT NOT NULL DEFAULT '',
  entity_id  TEXT NOT NULL DEFAULT '',
  ip         TEXT NOT NULL DEFAULT '',
  meta_json  TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS error_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  level      TEXT NOT NULL DEFAULT 'error',
  code       TEXT NOT NULL DEFAULT '',
  message    TEXT NOT NULL,
  path       TEXT NOT NULL DEFAULT '',
  stack      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS analytics_events (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  source     TEXT NOT NULL DEFAULT 'direct',
  user_id    INTEGER,
  session    TEXT NOT NULL DEFAULT '',
  value      INTEGER NOT NULL DEFAULT 0,
  meta_json  TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS scheduled_reports (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  report_key TEXT NOT NULL,
  cron       TEXT NOT NULL DEFAULT '0 6 * * 1',
  format     TEXT NOT NULL DEFAULT 'csv',
  recipients TEXT NOT NULL DEFAULT '',
  active     INTEGER NOT NULL DEFAULT 1,
  last_run_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS webhooks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  url         TEXT NOT NULL,
  events      TEXT NOT NULL DEFAULT 'order.created',
  secret      TEXT NOT NULL,
  active      INTEGER NOT NULL DEFAULT 1,
  failures    INTEGER NOT NULL DEFAULT 0,
  last_status INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS webhook_deliveries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  webhook_id  INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  event       TEXT NOT NULL,
  payload     TEXT NOT NULL DEFAULT '{}',
  signature   TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL DEFAULT 'pending',
  attempts    INTEGER NOT NULL DEFAULT 0,
  response_code INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS api_keys (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT NOT NULL,
  prefix     TEXT NOT NULL UNIQUE,
  key_hash   TEXT NOT NULL,
  scopes     TEXT NOT NULL DEFAULT 'read',
  owner_id   INTEGER,
  last_used_at TEXT,
  revoked_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cron_jobs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL UNIQUE,
  schedule    TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  retry_policy TEXT NOT NULL DEFAULT '1x',
  enabled     INTEGER NOT NULL DEFAULT 1,
  running     INTEGER NOT NULL DEFAULT 0,
  last_run_at TEXT,
  next_run_at TEXT,
  last_status TEXT NOT NULL DEFAULT 'idle',
  success_count INTEGER NOT NULL DEFAULT 0,
  failure_count INTEGER NOT NULL DEFAULT 0,
  avg_ms      INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS cron_runs (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id     INTEGER NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
  status     TEXT NOT NULL DEFAULT 'success',
  duration_ms INTEGER NOT NULL DEFAULT 0,
  output     TEXT NOT NULL DEFAULT '',
  triggered_by TEXT NOT NULL DEFAULT 'scheduler',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS backups (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  filename   TEXT NOT NULL,
  path       TEXT NOT NULL,
  size_bytes INTEGER NOT NULL DEFAULT 0,
  checksum   TEXT NOT NULL DEFAULT '',
  encrypted  INTEGER NOT NULL DEFAULT 1,
  trigger    TEXT NOT NULL DEFAULT 'manual',
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS seo_meta (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id   TEXT NOT NULL,
  title       TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  keywords    TEXT NOT NULL DEFAULT '',
  og_image    TEXT NOT NULL DEFAULT '',
  canonical   TEXT NOT NULL DEFAULT '',
  no_index    INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(entity_type, entity_id)
);

CREATE TABLE IF NOT EXISTS redirects (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  from_path  TEXT NOT NULL UNIQUE,
  to_path    TEXT NOT NULL,
  code       INTEGER NOT NULL DEFAULT 301,
  hits       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS currencies (
  code       TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  symbol     TEXT NOT NULL DEFAULT '',
  rate       REAL NOT NULL DEFAULT 1.0,     -- vs base currency
  decimals   INTEGER NOT NULL DEFAULT 0,
  active     INTEGER NOT NULL DEFAULT 1,
  is_base    INTEGER NOT NULL DEFAULT 0,
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS translations (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  locale     TEXT NOT NULL,
  key        TEXT NOT NULL,
  value      TEXT NOT NULL,
  UNIQUE(locale, key)
);

CREATE TABLE IF NOT EXISTS loyalty_accounts (
  user_id     INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  points      INTEGER NOT NULL DEFAULT 0,
  tier        TEXT NOT NULL DEFAULT 'bronze',
  lifetime    INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS loyalty_transactions (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  points     INTEGER NOT NULL,
  reason     TEXT NOT NULL DEFAULT '',
  expires_at TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS gift_cards (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  code        TEXT NOT NULL UNIQUE,
  initial_amount INTEGER NOT NULL,
  balance     INTEGER NOT NULL,
  currency    TEXT NOT NULL DEFAULT 'IRR',
  owner_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  redeemed_by INTEGER,
  redeemed_at TEXT,
  expires_at  TEXT,
  status      TEXT NOT NULL DEFAULT 'active',  -- active|redeemed|expired
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS referrals (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  referrer_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  referee_id  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  code        TEXT NOT NULL,
  reward      INTEGER NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'pending',
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS subscription_plans (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  code        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL, name_en TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  price       INTEGER NOT NULL DEFAULT 0,
  currency    TEXT NOT NULL DEFAULT 'IRR',
  interval    TEXT NOT NULL DEFAULT 'monthly', -- monthly|yearly
  trial_days  INTEGER NOT NULL DEFAULT 0,
  features_json TEXT NOT NULL DEFAULT '[]',
  active      INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS subscriptions (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  plan_id     INTEGER NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'active', -- active|past_due|cancelled|expired
  started_at  TEXT NOT NULL DEFAULT (datetime('now')),
  renews_at   TEXT,
  cancelled_at TEXT,
  failures    INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS subscription_invoices (
  id              INTEGER PRIMARY KEY AUTOINCREMENT,
  subscription_id INTEGER NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  amount          INTEGER NOT NULL,
  status          TEXT NOT NULL DEFAULT 'paid',
  period_start    TEXT, period_end TEXT,
  created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS import_jobs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  type        TEXT NOT NULL,
  filename    TEXT NOT NULL DEFAULT '',
  total_rows  INTEGER NOT NULL DEFAULT 0,
  processed   INTEGER NOT NULL DEFAULT 0,
  succeeded   INTEGER NOT NULL DEFAULT 0,
  failed      INTEGER NOT NULL DEFAULT 0,
  status      TEXT NOT NULL DEFAULT 'queued', -- queued|running|completed|failed
  errors_json TEXT NOT NULL DEFAULT '[]',
  created_by  INTEGER,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  finished_at TEXT
);

CREATE TABLE IF NOT EXISTS queue_jobs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  queue        TEXT NOT NULL DEFAULT 'default',
  name         TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'waiting', -- waiting|active|completed|failed|delayed
  attempts     INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  priority     INTEGER NOT NULL DEFAULT 0,
  error        TEXT NOT NULL DEFAULT '',
  result       TEXT NOT NULL DEFAULT '',
  run_at       TEXT NOT NULL DEFAULT (datetime('now')),
  started_at   TEXT, finished_at TEXT,
  duration_ms  INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_queue_status ON queue_jobs(queue, status);

CREATE TABLE IF NOT EXISTS feature_flags (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  key         TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  enabled     INTEGER NOT NULL DEFAULT 0,
  rollout     INTEGER NOT NULL DEFAULT 100,  -- percent
  roles       TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS ab_tests (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  key         TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  variants    TEXT NOT NULL DEFAULT 'A,B',
  status      TEXT NOT NULL DEFAULT 'running',
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS ab_results (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  test_id   INTEGER NOT NULL REFERENCES ab_tests(id) ON DELETE CASCADE,
  variant   TEXT NOT NULL,
  impressions INTEGER NOT NULL DEFAULT 0,
  conversions INTEGER NOT NULL DEFAULT 0,
  UNIQUE(test_id, variant)
);

CREATE TABLE IF NOT EXISTS rate_limits (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  bucket     TEXT NOT NULL,
  window_start TEXT NOT NULL,
  hits       INTEGER NOT NULL DEFAULT 0,
  UNIQUE(bucket, window_start)
);

CREATE VIRTUAL TABLE IF NOT EXISTS products_fts USING fts5(
  name, name_en, short_desc, description, sku, tokenize = 'unicode61'
);
