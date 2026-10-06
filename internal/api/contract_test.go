package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestEnvelopeShapeOnSuccess(t *testing.T) {
	env := testsupport.New(t)
	rec, e := call(t, env, http.MethodGet, "/api/v1/catalog/products?page=1&limit=5", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !e.Success || e.StatusCode != 200 || e.Timestamp == "" {
		t.Fatalf("malformed success envelope: %+v", e)
	}
	if e.Meta.Pagination == nil {
		t.Fatal("list endpoints must include meta.pagination")
	}
	if e.Meta.Pagination.Limit != 5 || e.Meta.Pagination.Page != 1 {
		t.Errorf("pagination echo wrong: %+v", e.Meta.Pagination)
	}
}

func TestEnvelopeShapeOnError(t *testing.T) {
	env := testsupport.New(t)
	rec, e := call(t, env, http.MethodGet, "/api/v1/catalog/products/999999", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if e.Success || e.Error == nil || e.Error.Code != "NOT_FOUND" {
		t.Fatalf("malformed error envelope: %+v", e)
	}
}

func TestAuthenticationAndRBAC(t *testing.T) {
	env := testsupport.New(t)

	rec, e := call(t, env, http.MethodGet, "/api/v1/admin/stats/overview", "", "")
	if rec.Code != http.StatusUnauthorized || e.Error == nil || e.Error.Code != "UNAUTHORIZED" {
		t.Fatalf("anonymous admin call = %d %+v", rec.Code, e.Error)
	}

	customer := bearer(t, env, "ali@example.com", "Customer@12345")
	rec, e = call(t, env, http.MethodGet, "/api/v1/admin/stats/overview", "", customer)
	if rec.Code != http.StatusForbidden || e.Error == nil || e.Error.Code != "FORBIDDEN" {
		t.Fatalf("customer admin call = %d %+v", rec.Code, e.Error)
	}

	admin := bearer(t, env, "admin@yourstore.com", "Admin@12345")
	rec, e = call(t, env, http.MethodGet, "/api/v1/admin/stats/overview", "", admin)
	if rec.Code != http.StatusOK || !e.Success {
		t.Fatalf("admin dashboard = %d %s", rec.Code, rec.Body.String())
	}
}

func TestValidationError(t *testing.T) {
	env := testsupport.New(t)
	rec, e := call(t, env, http.MethodPost, "/api/v1/auth/register", `{"email":""}`, "")
	if rec.Code != http.StatusBadRequest || e.Error == nil || e.Error.Code != "VALIDATION_ERROR" {
		t.Fatalf("validation = %d %+v", rec.Code, e.Error)
	}
}

func TestPublicCatalogEndpoints(t *testing.T) {
	env := testsupport.New(t)
	for _, p := range []string{
		"/api/v1/catalog/products",
		"/api/v1/catalog/products/1",
		"/api/v1/catalog/categories",
		"/api/v1/catalog/brands",
		"/api/v1/catalog/attributes",
		"/api/v1/search?q=macbook",
		"/api/v1/blog/posts",
		"/api/v1/cms/pages",
		"/api/v1/cms/faqs",
		"/api/v1/shipping/methods",
		"/api/v1/localization/currencies",
		"/api/v1/health",
	} {
		rec, e := call(t, env, http.MethodGet, p, "", "")
		if rec.Code != http.StatusOK || !e.Success {
			t.Errorf("GET %s = %d (%s)", p, rec.Code, e.Message)
		}
	}
}

func TestEveryModuleIsRegistered(t *testing.T) {
	env := testsupport.New(t)
	rec, e := call(t, env, http.MethodGet, "/api/v1/_routes", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var data struct {
		Routes []struct {
			Method  string `json:"method"`
			Pattern string `json:"pattern"`
			Module  string `json:"module"`
		} `json:"routes"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]int{}
	for _, r := range data.Routes {
		seen[r.Module]++
	}
	wantModules := []string{
		"01-auth", "02-cms", "03-catalog", "04-inventory", "05-cart", "06-checkout",
		"07-payments", "08-shipping", "09-discounts", "10-tax", "11-reviews", "12-wishlist",
		"13-blog", "14-vendors", "15-returns", "16-media", "17-notifications", "18-search",
		"19-admin", "20-integrations", "21-reports", "22-cron", "23-health", "24-maintenance",
		"25-seo", "26-localization", "27-loyalty", "28-subscriptions", "29-import-export",
		"30-queue", "31-feature-flags",
	}
	for _, m := range wantModules {
		if seen[m] == 0 {
			t.Errorf("module %s has no registered routes", m)
		}
	}
	if len(data.Routes) < 300 {
		t.Errorf("only %d routes registered", len(data.Routes))
	}
}

func TestErrorCodeRegistryIsComplete(t *testing.T) {
	env := testsupport.New(t)
	_, e := call(t, env, http.MethodGet, "/api/v1/_errors", "", "")
	var codes []struct {
		Code   string `json:"code"`
		Status int    `json:"status"`
	}
	if err := json.Unmarshal(e.Data, &codes); err != nil {
		var wrapped struct {
			Errors []struct {
				Code   string `json:"code"`
				Status int    `json:"status"`
			} `json:"errors"`
		}
		if err2 := json.Unmarshal(e.Data, &wrapped); err2 != nil {
			t.Fatalf("decode error registry: %v / %v (%s)", err, err2, e.Data)
		}
		codes = wrapped.Errors
	}
	want := map[string]int{
		"VALIDATION_ERROR": 400, "UNAUTHORIZED": 401, "FORBIDDEN": 403, "NOT_FOUND": 404,
		"CONFLICT": 409, "OUT_OF_STOCK": 422, "COUPON_INVALID": 422, "PAYMENT_FAILED": 402,
		"SHIPPING_UNAVAILABLE": 422, "RATE_LIMIT_EXCEEDED": 429, "IDEMPOTENCY_CONFLICT": 409,
		"CRON_JOB_NOT_FOUND": 404, "CRON_JOB_ALREADY_RUNNING": 409, "BACKUP_IN_PROGRESS": 409,
		"IMPORT_VALIDATION_FAILED": 422, "SUBSCRIPTION_PAYMENT_FAILED": 402,
		"GIFT_CARD_ALREADY_REDEEMED": 409, "FEATURE_FLAG_DISABLED": 403,
		"MAINTENANCE_MODE": 503, "INTERNAL_SERVER_ERROR": 500,
	}
	got := map[string]int{}
	for _, c := range codes {
		got[c.Code] = c.Status
	}
	for code, status := range want {
		if got[code] != status {
			t.Errorf("error code %s = %d, want %d", code, got[code], status)
		}
	}
}

func TestIdempotencyKeyIsHonoured(t *testing.T) {
	env := testsupport.New(t)
	token := bearer(t, env, "ali@example.com", "Customer@12345")

	body := `{"amount":50000,"gateway":"wallet"}`
	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/wallet/topup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Idempotency-Key", "test-key-0001")
		rec := httptest.NewRecorder()
		env.Handler.ServeHTTP(rec, req)
		return rec.Code
	}
	first := do()
	second := do()
	if first >= 500 || second >= 500 {
		t.Fatalf("top-up returned server errors: %d / %d", first, second)
	}

	n, err := env.DB.Count(context.Background(),
		`SELECT COUNT(*) FROM idempotency_keys WHERE key='test-key-0001'`)
	if err == nil && n > 1 {
		t.Errorf("idempotency key stored %d times", n)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	env := testsupport.New(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rec := httptest.NewRecorder()
	env.Handler.ServeHTTP(rec, req)

	for _, h := range []string{
		"Content-Security-Policy", "X-Content-Type-Options",
		"Referrer-Policy", "X-Request-Id",
	} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing security header %s", h)
		}
	}
}
