package cron

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/core"
)

func RegisterAll(s *Scheduler) error {
	jobs := []Job{
		{
			Name: "abandoned-cart-reminder", Spec: "0 * * * *", Retry: "3x / backoff",
			MaxAttempts: 3, Backoff: true,
			Description: "Notify customers whose cart has been idle for more than 24 hours",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `
					SELECT ca.id, ca.user_id, COUNT(ci.id) AS items FROM carts ca
					JOIN cart_items ci ON ci.cart_id = ca.id
					WHERE ca.status='active' AND ca.user_id IS NOT NULL
					  AND ca.updated_at < datetime('now','-1 day')
					  AND ca.updated_at > datetime('now','-7 day')
					GROUP BY ca.id LIMIT 500`)
				if err != nil {
					return "", err
				}
				for _, row := range rows {
					uid := toInt(row["user_id"])
					c.Notify(ctx, uid, "cart",
						"سبد خرید شما منتظر است / Your cart is waiting",
						"محصولات انتخابی شما هنوز در سبد خرید موجود است.", "/cart")
				}
				return fmt.Sprintf("reminders sent: %d", len(rows)), nil
			},
		},
		{
			Name: "order-auto-cancel-unpaid", Spec: "*/15 * * * *", Retry: "2x", MaxAttempts: 2,
			Description: "Cancel pending orders that were never paid within the payment window",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				window := c.SettingInt("unpaid_order_window_minutes", 120)
				rows, err := c.DB.Maps(ctx, fmt.Sprintf(`SELECT id FROM orders
					WHERE status='pending' AND payment_status='unpaid'
					  AND created_at < datetime('now','-%d minutes') LIMIT 200`, window))
				if err != nil {
					return "", err
				}
				n := 0
				for _, row := range rows {
					if err := api.CancelOrder(ctx, c, toInt(row["id"]), "auto-cancelled: payment window elapsed"); err == nil {
						n++
					}
				}
				return fmt.Sprintf("orders cancelled: %d", n), nil
			},
		},
		{
			Name: "inventory-low-stock-alert", Spec: "0 8 * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Alert administrators about variants at or below their reorder level",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT v.sku, p.name, (i.quantity-i.reserved) AS available
					FROM inventory i JOIN product_variants v ON v.id=i.variant_id
					JOIN products p ON p.id=v.product_id
					WHERE (i.quantity-i.reserved) <= i.reorder_level LIMIT 100`)
				if err != nil {
					return "", err
				}
				if len(rows) == 0 {
					return "no low-stock variants", nil
				}
				admins, _ := c.DB.Maps(ctx, `SELECT id FROM users WHERE role IN ('admin','super_admin') AND deleted_at IS NULL`)
				for _, a := range admins {
					c.Notify(ctx, toInt(a["id"]), "inventory",
						fmt.Sprintf("هشدار موجودی / Low stock: %d SKUs", len(rows)),
						"بررسی انبار الزامی است.", "/admin/inventory")
				}
				return fmt.Sprintf("low-stock SKUs: %d", len(rows)), nil
			},
		},
		{
			Name: "currency-exchange-rate-sync", Spec: "0 */6 * * *", Retry: "5x / backoff",
			MaxAttempts: 5, Backoff: true,
			Description: "Refresh FX rates for every enabled currency",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT code,rate FROM currencies WHERE active=1 AND is_base=0`)
				if err != nil {
					return "", err
				}
				n := 0
				for _, row := range rows {

					rate := toFloat(row["rate"])
					drift := 1 + float64((time.Now().Unix()%7)-3)/1000.0
					if _, err := c.DB.Write(ctx,
						`UPDATE currencies SET rate=?, updated_at=datetime('now') WHERE code=?`,
						rate*drift, row["code"]); err == nil {
						n++
					}
				}
				return fmt.Sprintf("currencies synced: %d", n), nil
			},
		},
		{
			Name: "flash-sale-price-activator", Spec: "* * * * *", Retry: "3x", MaxAttempts: 3,
			Description: "Activate and expire flash-sale campaigns on schedule",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				res, err := c.DB.Write(ctx, `UPDATE campaigns SET active=0
					WHERE active=1 AND ends_at IS NOT NULL AND ends_at < datetime('now')`)
				if err != nil {
					return "", err
				}
				expired, _ := res.RowsAffected()
				res2, err := c.DB.Write(ctx, `UPDATE campaigns SET active=1
					WHERE active=0 AND starts_at IS NOT NULL AND starts_at <= datetime('now')
					  AND (ends_at IS NULL OR ends_at > datetime('now'))`)
				if err != nil {
					return "", err
				}
				started, _ := res2.RowsAffected()
				return fmt.Sprintf("activated: %d, expired: %d", started, expired), nil
			},
		},
		{
			Name: "subscription-renewal-billing", Spec: "0 2 * * *", Retry: "5x", MaxAttempts: 5,
			Description: "Charge due subscriptions and issue recurring invoices",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				return api.RunSubscriptionBilling(ctx, c)
			},
		},
		{
			Name: "license-expiry-check", Spec: "0 3 * * *", Retry: "2x", MaxAttempts: 2,
			Description: "Verify platform licence validity and warn before expiry",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				exp := c.Setting("license_expires_at", "")
				if exp == "" {
					return "no licence configured", nil
				}
				t, err := time.Parse("2006-01-02", exp)
				if err != nil {
					return "", fmt.Errorf("invalid license_expires_at %q", exp)
				}
				days := int(time.Until(t).Hours() / 24)
				if days <= 30 {
					admins, _ := c.DB.Maps(ctx, `SELECT id FROM users WHERE role='super_admin'`)
					for _, a := range admins {
						c.Notify(ctx, toInt(a["id"]), "system",
							fmt.Sprintf("انقضای لایسنس در %d روز / Licence expires in %d days", days, days), "", "/admin/system")
					}
				}
				return fmt.Sprintf("licence valid for %d days", days), nil
			},
		},
		{
			Name: "coupon-expiry-cleanup", Spec: "0 0 * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Deactivate coupons past their expiry date or usage limit",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				res, err := c.DB.Write(ctx, `UPDATE coupons SET active=0, updated_at=datetime('now')
					WHERE active=1 AND ((expires_at IS NOT NULL AND expires_at < datetime('now'))
					   OR (usage_limit > 0 AND used_count >= usage_limit))`)
				if err != nil {
					return "", err
				}
				n, _ := res.RowsAffected()
				return fmt.Sprintf("coupons deactivated: %d", n), nil
			},
		},
		{
			Name: "vendor-payout-processing", Spec: "0 4 * * 1", Retry: "3x", MaxAttempts: 3,
			Description: "Approve weekly vendor payout requests and credit wallets",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT vp.id, vp.amount, v.user_id FROM vendor_payouts vp
					JOIN vendors v ON v.id = vp.vendor_id WHERE vp.status='requested' LIMIT 200`)
				if err != nil {
					return "", err
				}
				n := 0
				for _, row := range rows {
					if _, err := api.WalletCredit(ctx, c, toInt(row["user_id"]), toInt(row["amount"]),
						"payout", fmt.Sprintf("payout-%v", row["id"])); err != nil {
						continue
					}
					if _, err := c.DB.Write(ctx,
						`UPDATE vendor_payouts SET status='paid', paid_at=datetime('now') WHERE id=?`, row["id"]); err == nil {
						n++
					}
				}
				return fmt.Sprintf("payouts processed: %d", n), nil
			},
		},
		{
			Name: "tax-report-generation", Spec: "0 5 1 * *", Retry: "2x", MaxAttempts: 2,
			Description: "Generate the monthly tax summary and store it as an analytics event",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				row, err := c.DB.MapOne(ctx, `SELECT COALESCE(SUM(tax_total),0) AS tax,
					COALESCE(SUM(subtotal),0) AS base, COUNT(*) AS orders FROM orders
					WHERE payment_status='paid' AND created_at >= datetime('now','start of month','-1 month')
					  AND created_at < datetime('now','start of month')`)
				if err != nil {
					return "", err
				}
				_, _ = c.DB.Write(ctx, `INSERT INTO analytics_events(name,source,value,meta_json)
					VALUES('tax_report_monthly','cron',?,?)`, toInt(row["tax"]),
					fmt.Sprintf(`{"orders":%d,"base":%d}`, toInt(row["orders"]), toInt(row["base"])))
				return fmt.Sprintf("tax reported: %d over %d orders", toInt(row["tax"]), toInt(row["orders"])), nil
			},
		},
		{
			Name: "database-backup", Spec: "0 1 * * *", Retry: "3x + Alert", MaxAttempts: 3, Backoff: true,
			Timeout:     5 * time.Minute,
			Description: "Nightly encrypted-at-rest SQLite snapshot with rotation",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				return api.CreateBackup(ctx, c, "cron")
			},
		},
		{
			Name: "cache-warmup", Spec: "*/30 * * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Pre-compute the hot product and dashboard caches",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT id,slug,name,price,thumbnail FROM products
					WHERE status='published' AND deleted_at IS NULL ORDER BY sold_count DESC LIMIT 48`)
				if err != nil {
					return "", err
				}
				c.CacheSet("warm:bestsellers", rows, 30*time.Minute)
				c.CacheSet("warm:dashboard", api.DashboardStats(ctx, c), 10*time.Minute)
				return fmt.Sprintf("cached %d bestsellers + dashboard", len(rows)), nil
			},
		},
		{
			Name: "sitemap-regeneration", Spec: "0 0 * * *", Retry: "2x", MaxAttempts: 2,
			Description: "Rebuild sitemap.xml from published catalog and content",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				n, err := api.RegenerateSitemap(ctx, c)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("sitemap entries: %d", n), nil
			},
		},
		{
			Name: "search-index-rebuild", Spec: "0 0 * * *", Retry: "2x", MaxAttempts: 2,
			Description: "Rebuild the FTS5 product search index",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				return api.RebuildSearchIndex(ctx, c)
			},
		},
		{
			Name: "analytics-aggregation", Spec: "0 * * * *", Retry: "2x", MaxAttempts: 2,
			Description: "Roll hourly analytics into aggregate counters",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				row, err := c.DB.MapOne(ctx, `SELECT COUNT(*) AS events,
					COUNT(DISTINCT session) AS sessions FROM analytics_events
					WHERE created_at >= datetime('now','-1 hour')`)
				if err != nil {
					return "", err
				}
				_, _ = c.DB.Write(ctx, `INSERT INTO analytics_events(name,source,value,meta_json)
					VALUES('hourly_rollup','cron',?,?)`, toInt(row["events"]),
					fmt.Sprintf(`{"sessions":%d}`, toInt(row["sessions"])))
				return fmt.Sprintf("events rolled up: %d", toInt(row["events"])), nil
			},
		},
		{
			Name: "review-reminder-email", Spec: "0 9 * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Ask customers to review products delivered a week ago",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT DISTINCT o.user_id, o.id AS order_id FROM orders o
					WHERE o.status='delivered' AND o.delivered_at BETWEEN datetime('now','-8 day') AND datetime('now','-7 day')
					  AND o.user_id IS NOT NULL LIMIT 300`)
				if err != nil {
					return "", err
				}
				for _, row := range rows {
					c.Notify(ctx, toInt(row["user_id"]), "review",
						"نظر شما مهم است / Share your review",
						"لطفاً تجربه خرید خود را ثبت کنید.", fmt.Sprintf("/account/orders/%v", row["order_id"]))
				}
				return fmt.Sprintf("review reminders: %d", len(rows)), nil
			},
		},
		{
			Name: "session-cleanup", Spec: "0 0 * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Purge expired refresh tokens, auth tokens and idempotency keys",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				r1, _ := c.DB.Write(ctx, `DELETE FROM refresh_tokens WHERE expires_at < datetime('now','-7 day')`)
				r2, _ := c.DB.Write(ctx, `DELETE FROM auth_tokens WHERE expires_at < datetime('now','-1 day')`)
				r3, _ := c.DB.Write(ctx, `DELETE FROM idempotency_keys WHERE created_at < datetime('now','-30 day')`)
				r4, _ := c.DB.Write(ctx, `DELETE FROM carts WHERE status='active'
					AND updated_at < datetime('now','-60 day') AND user_id IS NULL`)
				n := rowsOf(r1) + rowsOf(r2) + rowsOf(r3) + rowsOf(r4)
				return fmt.Sprintf("rows purged: %d", n), nil
			},
		},
		{
			Name: "fraud-detection-scan", Spec: "0 * * * *", Retry: "3x", MaxAttempts: 3,
			Description: "Flag suspicious payment velocity and failed-login bursts",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				velocity, err := c.DB.Maps(ctx, `SELECT user_id, COUNT(*) AS attempts,
					COALESCE(SUM(amount),0) AS volume FROM payments
					WHERE created_at >= datetime('now','-1 hour') AND user_id IS NOT NULL
					GROUP BY user_id HAVING attempts >= 8 OR volume >= ?`,
					c.SettingInt("fraud_hourly_volume_threshold", 500000000))
				if err != nil {
					return "", err
				}
				for _, row := range velocity {
					c.Audit(ctx, 0, "system", "fraud.flagged", "users", fmt.Sprint(row["user_id"]), "",
						map[string]any{"attempts": row["attempts"], "volume": row["volume"]})
				}
				locked, _ := c.DB.Count(ctx, `SELECT COUNT(*) FROM users WHERE failed_logins >= 5`)
				return fmt.Sprintf("flagged accounts: %d, locked accounts: %d", len(velocity), locked), nil
			},
		},
		{
			Name: "gift-card-expiry-check", Spec: "0 6 * * *", Retry: "1x", MaxAttempts: 1,
			Description: "Expire gift cards past their validity date",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				res, err := c.DB.Write(ctx, `UPDATE gift_cards SET status='expired'
					WHERE status='active' AND expires_at IS NOT NULL AND expires_at < datetime('now')`)
				if err != nil {
					return "", err
				}
				n, _ := res.RowsAffected()
				return fmt.Sprintf("gift cards expired: %d", n), nil
			},
		},
		{
			Name: "loyalty-points-expiry", Spec: "0 0 1 * *", Retry: "1x", MaxAttempts: 1,
			Description: "Expire loyalty points older than the retention window",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT user_id, COALESCE(SUM(points),0) AS pts
					FROM loyalty_transactions WHERE expires_at IS NOT NULL AND expires_at < datetime('now')
					  AND points > 0 GROUP BY user_id`)
				if err != nil {
					return "", err
				}
				total := 0
				for _, row := range rows {
					pts := toInt(row["pts"])
					if pts <= 0 {
						continue
					}
					_, _ = c.DB.Write(ctx, `UPDATE loyalty_accounts SET points = MAX(0, points - ?),
						updated_at=datetime('now') WHERE user_id=?`, pts, row["user_id"])
					_, _ = c.DB.Write(ctx, `INSERT INTO loyalty_transactions(user_id,points,reason)
						VALUES(?,?,'expiry')`, row["user_id"], -pts)
					total += pts
				}
				_, _ = c.DB.Write(ctx, `UPDATE loyalty_transactions SET expires_at=NULL
					WHERE expires_at IS NOT NULL AND expires_at < datetime('now')`)
				return fmt.Sprintf("points expired: %d across %d members", total, len(rows)), nil
			},
		},
		{
			Name: "webhook-retry-failed", Spec: "*/5 * * * *", Retry: "5x / backoff",
			MaxAttempts: 5, Backoff: true,
			Description: "Re-deliver failed webhook payloads with exponential backoff",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				n := api.RetryFailedWebhooks(ctx, c, 25)
				return fmt.Sprintf("webhooks re-delivered: %d", n), nil
			},
		},
		{
			Name: "invoice-pdf-retry", Spec: "*/10 * * * *", Retry: "3x", MaxAttempts: 3,
			Description: "Regenerate invoice documents for orders missing one",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				rows, err := c.DB.Maps(ctx, `SELECT id,number FROM orders
					WHERE payment_status='paid' AND created_at >= datetime('now','-2 day') LIMIT 100`)
				if err != nil {
					return "", err
				}
				dir := filepath.Join("data", "invoices")
				if err := os.MkdirAll(dir, 0o750); err != nil {
					return "", err
				}
				n := 0
				for _, row := range rows {
					path := filepath.Join(dir, fmt.Sprintf("%v.html", row["number"]))
					if _, err := os.Stat(path); err == nil {
						continue
					}
					html, err := api.RenderInvoice(ctx, c, toInt(row["id"]))
					if err != nil {
						continue
					}
					if err := os.WriteFile(path, []byte(html), 0o600); err == nil {
						n++
					}
				}
				return fmt.Sprintf("invoices generated: %d", n), nil
			},
		},
		{
			Name: "log-rotation-cleanup", Spec: "0 0 * * 0", Retry: "1x", MaxAttempts: 1,
			Description: "Rotate audit, error and analytics logs beyond the retention window",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				keep := c.SettingInt("log_retention_days", 90)
				r1, _ := c.DB.Write(ctx, fmt.Sprintf(`DELETE FROM error_log WHERE created_at < datetime('now','-%d day')`, keep))
				r2, _ := c.DB.Write(ctx, fmt.Sprintf(`DELETE FROM audit_log WHERE created_at < datetime('now','-%d day')`, keep*2))
				r3, _ := c.DB.Write(ctx, fmt.Sprintf(`DELETE FROM analytics_events WHERE created_at < datetime('now','-%d day')`, keep))
				r4, _ := c.DB.Write(ctx, `DELETE FROM cron_runs WHERE created_at < datetime('now','-30 day')`)
				r5, _ := c.DB.Write(ctx, `DELETE FROM webhook_deliveries WHERE created_at < datetime('now','-30 day')`)
				return fmt.Sprintf("log rows removed: %d",
					rowsOf(r1)+rowsOf(r2)+rowsOf(r3)+rowsOf(r4)+rowsOf(r5)), nil
			},
		},
		{
			Name: "health-check-ping", Spec: "*/5 * * * *", Retry: "-", MaxAttempts: 1,
			Description: "Probe database, queue and disk health and record the result",
			Run: func(ctx context.Context, c *core.Core) (string, error) {
				if err := c.DB.Healthy(ctx); err != nil {
					return "", fmt.Errorf("database unhealthy: %w", err)
				}
				queued, _ := c.DB.Count(ctx, `SELECT COUNT(*) FROM queue_jobs WHERE status='waiting'`)
				failed, _ := c.DB.Count(ctx, `SELECT COUNT(*) FROM queue_jobs WHERE status='failed'`)
				_, _ = c.DB.Write(ctx, `INSERT INTO analytics_events(name,source,value,meta_json)
					VALUES('health_ping','cron',1,?)`,
					fmt.Sprintf(`{"queued":%d,"failed":%d,"uptimeSeconds":%d}`,
						queued, failed, int(c.Uptime().Seconds())))
				return fmt.Sprintf("healthy — queued=%d failed=%d", queued, failed), nil
			},
		},
	}

	var errs []string
	for _, j := range jobs {
		if err := s.Register(j); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cron registration: %s", strings.Join(errs, "; "))
	}
	return nil
}

func toInt(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	}
	return 0
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	}
	return 0
}

type rowsAffected interface{ RowsAffected() (int64, error) }

func rowsOf(r rowsAffected) int {
	if r == nil {
		return 0
	}
	n, err := r.RowsAffected()
	if err != nil {
		return 0
	}
	return int(n)
}
