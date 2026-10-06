package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

type routeInfo struct {
	Method  string `json:"method"`
	Pattern string `json:"pattern"`
	Access  string `json:"access"`
	Module  string `json:"module"`
}

func allRoutes(t *testing.T, env *testsupport.Env) []routeInfo {
	t.Helper()
	rec := env.Request(http.MethodGet, "/api/v1/_routes", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("route table = %d", rec.Code)
	}
	var env2 struct {
		Data struct {
			Count  int         `json:"count"`
			Routes []routeInfo `json:"routes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env2); err != nil {
		t.Fatalf("decode route table: %v", err)
	}
	if len(env2.Data.Routes) != env2.Data.Count || env2.Data.Count == 0 {
		t.Fatalf("route table inconsistent: count=%d len=%d", env2.Data.Count, len(env2.Data.Routes))
	}
	return env2.Data.Routes
}

type tokenSet struct {
	customer, vendor, admin string
}

func newTokens(t *testing.T, env *testsupport.Env) tokenSet {
	t.Helper()
	return tokenSet{
		customer: bearer(t, env, "ali@example.com", "Customer@12345"),
		vendor:   bearer(t, env, "vendor1@yourstore.com", "Vendor@12345"),

		admin: bearer(t, env, "admin@yourstore.com", "Admin@12345"),
	}
}

func (ts tokenSet) forAccess(access string) string {
	switch access {
	case "public":
		return ""
	case "authenticated":
		return ts.customer
	case "vendor":
		return ts.vendor
	default:
		return ts.admin
	}
}

var paramRe = regexp.MustCompile(`\{(\w+)\}`)

func nonJSON(pattern string) bool {
	switch {
	case strings.HasPrefix(pattern, "/api/v1/admin/export/"),
		strings.HasPrefix(pattern, "/api/v1/admin/import/"),
		strings.HasSuffix(pattern, "/export"),
		strings.HasSuffix(pattern, "/download"),
		strings.HasSuffix(pattern, "/invoice"),
		pattern == "/api/v1/blog/rss",
		pattern == "/api/v1/admin/system/metrics":
		return true
	}
	return false
}

type fixtures struct {
	orderID, paymentID, returnID, subscriptionID, addressID, orderItemID int
	reviewID, mediaID, wishlistProductID                                 int
	customerID, otherCustomerID                                          int
}

func loadFixtures(t *testing.T, env *testsupport.Env) fixtures {
	t.Helper()
	ctx := context.Background()
	const customerID = 7
	one := func(q string, args ...any) int {
		v, err := env.DB.Scalar(ctx, q, args...)
		if err != nil || !v.Valid {
			return 1
		}
		n, _ := strconv.Atoi(v.String)
		if n == 0 {
			return 1
		}
		return n
	}
	return fixtures{
		customerID: customerID,

		otherCustomerID:   one(`SELECT id FROM users WHERE role='customer' AND id<>? ORDER BY id LIMIT 1`, customerID),
		reviewID:          one(`SELECT id FROM reviews WHERE user_id=? ORDER BY id LIMIT 1`, customerID),
		mediaID:           one(`SELECT id FROM media WHERE owner_id=? ORDER BY id LIMIT 1`, customerID),
		wishlistProductID: one(`SELECT product_id FROM wishlist_items WHERE user_id=? ORDER BY id LIMIT 1`, customerID),
		orderID:           one(`SELECT id FROM orders WHERE user_id=? ORDER BY id LIMIT 1`, customerID),
		paymentID:         one(`SELECT p.id FROM payments p JOIN orders o ON o.id=p.order_id WHERE o.user_id=? ORDER BY p.id LIMIT 1`, customerID),
		returnID:          one(`SELECT r.id FROM returns r JOIN orders o ON o.id=r.order_id WHERE o.user_id=? ORDER BY r.id LIMIT 1`, customerID),
		subscriptionID:    one(`SELECT id FROM subscriptions WHERE user_id=? ORDER BY id LIMIT 1`, customerID),
		addressID:         one(`SELECT id FROM addresses WHERE user_id=? ORDER BY id LIMIT 1`, customerID),
		orderItemID:       one(`SELECT oi.id FROM order_items oi JOIN orders o ON o.id=oi.order_id WHERE o.user_id=? ORDER BY oi.id LIMIT 1`, customerID),
	}
}

func (f fixtures) resolve(pattern string) string {
	out := pattern

	owned := map[string]int{
		"/api/v1/orders/{id}":               f.orderID,
		"/api/v1/orders/{id}/invoice":       f.orderID,
		"/api/v1/orders/{id}/track":         f.orderID,
		"/api/v1/orders/{id}/cancel":        f.orderID,
		"/api/v1/orders/{id}/reorder":       f.orderID,
		"/api/v1/payments/{id}":             f.paymentID,
		"/api/v1/payments/{id}/refund":      f.paymentID,
		"/api/v1/returns/{id}":              f.returnID,
		"/api/v1/returns/{id}/cancel":       f.returnID,
		"/api/v1/subscriptions/{id}":        f.subscriptionID,
		"/api/v1/subscriptions/{id}/pause":  f.subscriptionID,
		"/api/v1/subscriptions/{id}/resume": f.subscriptionID,
		"/api/v1/subscriptions/{id}/cancel": f.subscriptionID,
		"/api/v1/users/me/addresses/{id}":   f.addressID,
		"/api/v1/reviews/{id}":              f.reviewID,
		"/api/v1/media/{id}":                f.mediaID,
		"/api/v1/wishlist/{productId}":      f.wishlistProductID,
	}
	if id, ok := owned[pattern]; ok {
		return paramRe.ReplaceAllString(pattern, strconv.Itoa(id))
	}
	if id, ok := owned[pattern]; ok {
		return strings.Replace(pattern, "{id}", strconv.Itoa(id), 1)
	}

	if strings.HasPrefix(pattern, "/api/v1/admin/users/{id}") {
		return strings.Replace(pattern, "{id}", strconv.Itoa(f.otherCustomerID), 1)
	}

	specific := map[string]string{
		"/api/v1/cms/menus/{code}/tree":              "main",
		"/api/v1/shipping/track/{trackingCode}":      "TRK-1001",
		"/api/v1/localization/translations/{locale}": "fa",
		"/api/v1/admin/seo/meta/{entityType}/{id}":   "product",
		"/api/v1/admin/export/{type}":                "products",
		"/api/v1/admin/import/{type}/template":       "products",
		"/api/v1/admin/queues/{name}/jobs":           "webhook",
		"/api/v1/admin/queues/{name}/purge":          "webhook",
		"/api/v1/admin/queues/{name}/retry-failed":   "webhook",
		"/api/v1/feature-flags/{key}":                "new-checkout",
		"/api/v1/ab-tests/{key}/assign":              "checkout-button",
		"/api/v1/admin/ab-tests/{key}/results":       "checkout-button",
		"/api/v1/admin/cache/{key}":                  "catalog",
	}
	for pat, val := range specific {
		if pattern == pat {

			out = paramRe.ReplaceAllStringFunc(out, func(m string) string {
				name := m[1 : len(m)-1]
				if name == "id" {
					return "1"
				}
				return val
			})
			return out
		}
	}
	return paramRe.ReplaceAllStringFunc(out, func(m string) string {
		switch m[1 : len(m)-1] {
		case "locale":
			return "fa"
		case "code", "key", "name", "type", "entityType":
			return "main"
		case "trackingCode":
			return "TRK-1001"
		default:
			return "1"
		}
	})
}

func bodyFor(method, pattern string) string {
	if method == http.MethodGet || method == http.MethodDelete {
		return ""
	}
	exact := map[string]string{
		"/api/v1/auth/login":            `{"email":"ali@example.com","password":"Customer@12345"}`,
		"/api/v1/auth/register":         `{"email":"sweep-new@example.com","password":"Str0ng@Pass1","name":"Sweep"}`,
		"/api/v1/auth/forgot-password":  `{"email":"ali@example.com"}`,
		"/api/v1/auth/reset-password":   `{"token":"nope","password":"Str0ng@Pass1"}`,
		"/api/v1/auth/refresh":          `{}`,
		"/api/v1/auth/change-password":  `{"currentPassword":"Customer@12345","newPassword":"Str0ng@Pass2"}`,
		"/api/v1/auth/2fa/verify":       `{"code":"000000"}`,
		"/api/v1/cart/items":            `{"productId":1,"quantity":1}`,
		"/api/v1/cart/coupon":           `{"code":"VIP20"}`,
		"/api/v1/cart/merge":            `{"token":"other"}`,
		"/api/v1/checkout/complete":     `{"shippingMethodId":1,"paymentMethod":"wallet","address":{"recipient":"A","phone":"09120000000","country":"IR","province":"Tehran","city":"Tehran","postal_code":"1234567890","line1":"St 1"}}`,
		"/api/v1/checkout/validate":     `{"shippingMethodId":1}`,
		"/api/v1/payments/verify":       `{"reference":"PAY-DOESNOTEXIST","outcome":"success"}`,
		"/api/v1/payments/wallet/topup": `{"amount":100000}`,
		"/api/v1/shipping/quote":        `{"shippingMethodId":1,"weightG":500,"province":"Tehran"}`,
		"/api/v1/discounts/validate":    `{"code":"VIP20","subtotal":50000000}`,
		"/api/v1/tax/calculate":         `{"amount":1000000,"country":"IR"}`,
		"/api/v1/reviews":               `{"productId":1,"rating":5,"title":"Good","body":"Nice product indeed"}`,
		"/api/v1/returns":               `{"orderId":1,"reason":"damaged","items":[{"orderItemId":1,"quantity":1}]}`,
		"/api/v1/wishlist/items":        `{"productId":3}`,
		"/api/v1/compare/items":         `{"productId":3}`,
		"/api/v1/loyalty/redeem":        `{"points":10}`,
		"/api/v1/giftcards/redeem":      `{"code":"GC-UNKNOWN"}`,
		"/api/v1/referrals/apply":       `{"code":"UNKNOWN"}`,
		"/api/v1/newsletter/subscribe":  `{"email":"sweep@example.com"}`,
		"/api/v1/search/suggest":        `{"q":"mac"}`,
	}
	if b, ok := exact[pattern]; ok {
		return b
	}

	switch {
	case strings.Contains(pattern, "/addresses"):
		return `{"recipient":"Sweep","phone":"09120000000","country":"IR","province":"Tehran","city":"Tehran","postal_code":"1234567890","line1":"Sweep St 1"}`
	case strings.Contains(pattern, "/products"):
		return `{"name":"Sweep Product","name_en":"Sweep Product","slug":"sweep-product","price":1000000,"status":"draft"}`
	case strings.Contains(pattern, "/categories"):
		return `{"name":"Sweep Cat","name_en":"Sweep Cat","slug":"sweep-cat"}`
	case strings.Contains(pattern, "/brands"):
		return `{"name":"Sweep Brand","name_en":"Sweep Brand","slug":"sweep-brand"}`
	case strings.Contains(pattern, "/coupons"):
		return `{"code":"SWEEP10","type":"percent","value":10}`
	case strings.Contains(pattern, "/pages"):
		return `{"title":"Sweep Page","slug":"sweep-page","body":"Hello"}`
	case strings.Contains(pattern, "/posts"):
		return `{"title":"Sweep Post","slug":"sweep-post","body":"Hello","status":"draft"}`
	case strings.Contains(pattern, "/warehouses"):
		return `{"name":"Sweep WH","code":"SWEEP","city":"Tehran"}`
	case strings.Contains(pattern, "/webhooks"):
		return `{"name":"Sweep Hook","url":"https://example.invalid/hook","events":"*"}`
	case strings.Contains(pattern, "/feature-flags"):
		return `{"key":"sweep-flag","description":"Sweep","enabled":true}`
	case strings.Contains(pattern, "/ab-tests"):
		return `{"key":"sweep-test","name":"Sweep","variants":"a,b"}`
	case strings.Contains(pattern, "/faqs"):
		return `{"question":"Q?","answer":"A."}`
	case strings.Contains(pattern, "/banners"):
		return `{"title":"Sweep","position":"hero","link":"/shop"}`
	case strings.Contains(pattern, "/menus"):
		return `{"code":"sweep","name":"Sweep"}`
	case strings.Contains(pattern, "/zones"):
		return `{"name":"Sweep Zone","provinces":"Tehran"}`
	case strings.Contains(pattern, "/rules"):
		return `{"name":"Sweep Tax","country":"IR","rate":9}`
	case strings.Contains(pattern, "/notifications"):
		return `{"userId":1,"type":"system","title":"Sweep","body":"Hello"}`
	case strings.Contains(pattern, "/vendors"):
		return `{"shop_name":"Sweep Shop","slug":"sweep-shop","email":"sweep@vendor.test"}`
	case strings.Contains(pattern, "/subscription"):
		return `{"planId":1}`
	case strings.Contains(pattern, "/status"):
		return `{"status":"pending"}`
	}
	return `{}`
}

func TestEveryReadEndpointResponds(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	tok := newTokens(t, env)
	routes := allRoutes(t, env)
	fx := loadFixtures(t, env)

	var checked int
	for _, rt := range routes {
		if rt.Method != http.MethodGet {
			continue
		}
		rt := rt
		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			target := fx.resolve(rt.Pattern)
			hdr := map[string]string{}
			if tk := tok.forAccess(rt.Access); tk != "" {
				hdr["Authorization"] = "Bearer " + tk
			}
			rec := env.Request(rt.Method, target, "", hdr)

			if rec.Code >= 500 {
				t.Fatalf("%s %s -> %d (server error)\n%.400s",
					rt.Method, target, rec.Code, rec.Body.String())
			}

			if rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
				t.Fatalf("%s %s as %q -> %d, privileged caller was rejected\n%.300s",
					rt.Method, target, rt.Access, rec.Code, rec.Body.String())
			}
			if nonJSON(rt.Pattern) {

				if ct := rec.Header().Get("Content-Type"); ct == "" {
					t.Errorf("%s %s: no Content-Type on a non-JSON response", rt.Method, target)
				}
				return
			}
			var e envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatalf("%s %s: not a JSON envelope: %v\n%.300s",
					rt.Method, target, err, rec.Body.String())
			}
			if e.StatusCode != rec.Code {
				t.Errorf("%s %s: envelope statusCode %d != HTTP %d", rt.Method, target, e.StatusCode, rec.Code)
			}
			if e.Timestamp == "" {
				t.Errorf("%s %s: envelope has no timestamp", rt.Method, target)
			}
			if e.Success != (rec.Code < 400) {
				t.Errorf("%s %s: success=%v with status %d", rt.Method, target, e.Success, rec.Code)
			}
			if !e.Success && (e.Error == nil || e.Error.Code == "") {
				t.Errorf("%s %s: failure carries no error code", rt.Method, target)
			}
		})
		checked++
	}
	if checked < 150 {
		t.Fatalf("only swept %d GET routes, expected the full read surface", checked)
	}
	t.Logf("swept %d GET endpoints", checked)
}

func TestEveryMutationEndpointResponds(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	tok := newTokens(t, env)
	routes := allRoutes(t, env)
	fx := loadFixtures(t, env)

	rank := map[string]int{http.MethodPost: 0, http.MethodPut: 1, http.MethodPatch: 2, http.MethodDelete: 3}
	var muts []routeInfo
	for _, rt := range routes {
		if _, ok := rank[rt.Method]; ok {
			muts = append(muts, rt)
		}
	}
	for i := 0; i < len(muts); i++ {
		for j := i + 1; j < len(muts); j++ {
			if rank[muts[j].Method] < rank[muts[i].Method] {
				muts[i], muts[j] = muts[j], muts[i]
			}
		}
	}

	skip := map[string]bool{

		"/api/v1/auth/logout":          true,
		"/api/v1/auth/logout-all":      true,
		"/api/v1/auth/change-password": true,

		"/api/v1/users/me": true,

		"/api/v1/admin/system/maintenance-mode": true,

		"/api/v1/admin/system/backup/{id}/restore": true,
	}

	var checked int
	for _, rt := range muts {
		if skip[rt.Pattern] {
			continue
		}
		rt := rt
		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			target := fx.resolve(rt.Pattern)
			hdr := map[string]string{"Idempotency-Key": fmt.Sprintf("sweep-%s-%s", rt.Method, rt.Pattern)}
			if tk := tok.forAccess(rt.Access); tk != "" {
				hdr["Authorization"] = "Bearer " + tk
			}
			rec := env.Request(rt.Method, target, bodyFor(rt.Method, rt.Pattern), hdr)

			if rec.Code == http.StatusServiceUnavailable && strings.HasPrefix(rt.Pattern, "/api/v1/admin/cron/") {
				var e envelope
				_ = json.Unmarshal(rec.Body.Bytes(), &e)
				if e.Error == nil || e.Error.Code != "CRON_SCHEDULER_UNAVAILABLE" {
					t.Fatalf("%s %s: 503 without the capability code: %.200s", rt.Method, target, rec.Body.String())
				}
				return
			}
			if rec.Code >= 500 {
				t.Fatalf("%s %s -> %d (server error)\n%.400s",
					rt.Method, target, rec.Code, rec.Body.String())
			}

			stepUp := rt.Pattern == "/api/v1/auth/2fa/verify" || rt.Pattern == "/api/v1/auth/refresh-token"
			if (rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden) && !stepUp {
				t.Fatalf("%s %s as %q -> %d, privileged caller was rejected\n%.300s",
					rt.Method, target, rt.Access, rec.Code, rec.Body.String())
			}
			if rec.Code == http.StatusNoContent || len(rec.Body.Bytes()) == 0 {
				return
			}
			if nonJSON(rt.Pattern) {
				if ct := rec.Header().Get("Content-Type"); ct == "" {
					t.Errorf("%s %s: no Content-Type on a non-JSON response", rt.Method, target)
				}
				return
			}
			var e envelope
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatalf("%s %s: not a JSON envelope: %v\n%.300s",
					rt.Method, target, err, rec.Body.String())
			}
			if e.Success != (rec.Code < 400) {
				t.Errorf("%s %s: success=%v with status %d", rt.Method, target, e.Success, rec.Code)
			}
			if !e.Success && (e.Error == nil || e.Error.Code == "") {
				t.Errorf("%s %s: failure carries no error code\n%.200s", rt.Method, target, rec.Body.String())
			}
		})
		checked++
	}
	if checked < 200 {
		t.Fatalf("only swept %d mutating routes, expected the full write surface", checked)
	}
	t.Logf("swept %d mutating endpoints", checked)
}

func TestProtectedEndpointsRejectAnonymousCallers(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	routes := allRoutes(t, env)
	fx := loadFixtures(t, env)

	var checked int
	for _, rt := range routes {
		if rt.Access == "public" {
			continue
		}
		rt := rt
		t.Run(rt.Method+" "+rt.Pattern, func(t *testing.T) {
			rec := env.Request(rt.Method, fx.resolve(rt.Pattern), bodyFor(rt.Method, rt.Pattern), nil)
			if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
				t.Errorf("%s %s is %q but answered %d to an anonymous caller",
					rt.Method, rt.Pattern, rt.Access, rec.Code)
			}
		})
		checked++
	}
	t.Logf("verified %d protected endpoints reject anonymous callers", checked)
}

func TestRoleBoundariesAreEnforced(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	tok := newTokens(t, env)
	routes := allRoutes(t, env)
	fx := loadFixtures(t, env)

	elevated := map[string]bool{"admin": true, "super_admin": true, "editor": true}

	var customerBlocked, vendorBlocked int
	for _, rt := range routes {
		if !elevated[rt.Access] {
			continue
		}
		target := fx.resolve(rt.Pattern)
		body := bodyFor(rt.Method, rt.Pattern)

		rec := env.Request(rt.Method, target, body, map[string]string{"Authorization": "Bearer " + tok.customer})
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("customer reached %s %s (%s) -> %d", rt.Method, rt.Pattern, rt.Access, rec.Code)
			continue
		}
		customerBlocked++

		rec = env.Request(rt.Method, target, body, map[string]string{"Authorization": "Bearer " + tok.vendor})
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
			t.Errorf("vendor reached %s %s (%s) -> %d", rt.Method, rt.Pattern, rt.Access, rec.Code)
			continue
		}
		vendorBlocked++
	}
	if customerBlocked == 0 || vendorBlocked == 0 {
		t.Fatal("role boundary sweep checked nothing")
	}
	t.Logf("customer blocked from %d elevated routes, vendor from %d", customerBlocked, vendorBlocked)
}
