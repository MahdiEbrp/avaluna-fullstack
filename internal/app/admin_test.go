package app_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestAdminRequiresStaff(t *testing.T) {
	env := testsupport.New(t)

	anon := env.Browser().Get("/admin")
	if anon.Code != http.StatusSeeOther {
		t.Errorf("anonymous /admin = %d, want a redirect to /login", anon.Code)
	}

	customer := login(t, env, customerEmail, customerPass)
	if rec := customer.Get("/admin"); rec.Code != http.StatusForbidden {
		t.Errorf("customer /admin = %d, want 403", rec.Code)
	}
}

func TestAdminSectionsRender(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	sections := []string{
		"/admin", "/admin/orders", "/admin/products", "/admin/customers", "/admin/inventory",
		"/admin/coupons", "/admin/vendors", "/admin/returns", "/admin/blog", "/admin/reviews",
		"/admin/media", "/admin/reports", "/admin/analytics", "/admin/audit", "/admin/cron",
		"/admin/queue", "/admin/webhooks", "/admin/logs", "/admin/settings",
	}
	for _, s := range sections {
		rec := b.Get(s)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d", s, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "</html>") {
			t.Errorf("GET %s did not render a complete document", s)
		}
	}
}

func TestAdminSearchAndFilter(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	rec := b.Get("/admin/orders?status=delivered")
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered orders = %d", rec.Code)
	}
	if rec := b.Get("/admin/products?q=mac"); rec.Code != http.StatusOK {
		t.Fatalf("product search = %d", rec.Code)
	}
}

func TestAdminReportsAllKeys(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	keys := []string{"revenue", "sales", "top-products", "customer-ltv", "inventory-valuation",
		"coupon-performance", "tax-summary", "abandoned-carts", "conversion-funnel",
		"vendor-commissions", "loyalty", "subscriptions", "returns-refunds", "search-terms",
		"traffic-sources", "profit-margin", "cohort-analysis", "stock-movements",
		"slow-moving-products", "payment-methods-breakdown"}
	for _, k := range keys {
		rec := b.Get("/admin/reports?key=" + k)
		if rec.Code != http.StatusOK {
			t.Errorf("report %s = %d", k, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "error:") {
			t.Errorf("report %s returned an error", k)
		}
	}
}

func TestAdminCronControls(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	page := b.Get("/admin/cron")
	if page.Code != http.StatusOK {
		t.Fatalf("cron page = %d", page.Code)
	}
	for _, job := range []string{"abandoned-cart-reminder", "database-backup", "health-check-ping",
		"webhook-retry-failed", "loyalty-points-expiry"} {
		if !strings.Contains(page.Body.String(), job) {
			t.Errorf("cron page is missing job %q", job)
		}
	}

	rec := b.Do(http.MethodPost, "/admin/cron/database-backup/run", nil, "")
	if rec.Code >= 500 {
		t.Errorf("cron run = %d, want a graceful response", rec.Code)
	}
}

func TestAdminSystemActions(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	for _, action := range []string{"cache-clear", "backup", "sitemap", "reindex", "optimize"} {
		rec := b.Do(http.MethodPost, "/admin/system/"+action, nil, "")
		if rec.Code != http.StatusOK {
			t.Errorf("system %s = %d", action, rec.Code)
			continue
		}
		if strings.Contains(rec.Body.String(), "rose-50") {
			t.Errorf("system %s reported an error: %s", action, rec.Body.String())
		}
	}
}

func TestAdminSettingsAndFlags(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	save := b.PostForm("/admin/settings", url.Values{"support_phone": {"021-12345678"}})
	if save.Code != http.StatusOK || !strings.Contains(save.Body.String(), "Saved") {
		t.Fatalf("settings save = %d %s", save.Code, save.Body.String())
	}
	if got := env.Core.Setting("support_phone", ""); got != "021-12345678" {
		t.Errorf("setting not persisted, got %q", got)
	}

	toggle := b.Do(http.MethodPost, "/admin/flags/ai_recommendations/toggle", nil, "")
	if toggle.Code != http.StatusOK {
		t.Fatalf("flag toggle = %d", toggle.Code)
	}
}

func TestMaintenanceModeBlocksStorefront(t *testing.T) {
	env := testsupport.New(t)
	admin := login(t, env, adminEmail, adminPass)

	if rec := admin.Do(http.MethodPost, "/admin/system/maintenance", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("enable maintenance = %d", rec.Code)
	}
	rec := env.Browser().Get("/shop")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("storefront during maintenance = %d, want 503", rec.Code)
	}

	if rec := admin.Get("/admin"); rec.Code != http.StatusOK {
		t.Errorf("admin during maintenance = %d, want 200", rec.Code)
	}
	admin.Do(http.MethodPost, "/admin/system/maintenance", nil, "")
}
