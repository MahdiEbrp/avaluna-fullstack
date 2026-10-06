package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func hammer(n int, fn func(i int)) {
	var gate sync.WaitGroup
	var done sync.WaitGroup
	gate.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			gate.Wait()
			fn(i)
		}(i)
	}
	gate.Done()
	done.Wait()
}

type statusCounter struct {
	ok, client, server atomic.Int32
}

func (s *statusCounter) record(code int) {
	switch {
	case code >= 500:
		s.server.Add(1)
	case code >= 400:
		s.client.Add(1)
	default:
		s.ok.Add(1)
	}
}

func (s *statusCounter) String() string {
	return fmt.Sprintf("2xx/3xx=%d 4xx=%d 5xx=%d", s.ok.Load(), s.client.Load(), s.server.Load())
}

func TestStressConcurrentCheckoutDoesNotDoubleCharge(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	token := bearer(t, env, "ali@example.com", "Customer@12345")

	mustExec(t, env, `DELETE FROM cart_items WHERE cart_id IN (SELECT id FROM carts WHERE user_id=7)`)
	const funded = 3_000_000_000
	mustExec(t, env, `UPDATE wallets SET balance=? WHERE user_id=7`, funded)

	debitedBefore := scalarInt(t, env, `
		SELECT COALESCE(-SUM(amount),0) FROM wallet_transactions WHERE user_id=7 AND amount < 0`)
	paidBefore := scalarInt(t, env, `
		SELECT COALESCE(SUM(amount),0) FROM payments
		WHERE user_id=7 AND gateway='wallet' AND status='succeeded'`)

	var counts statusCounter
	const attempts = 24

	hammer(attempts, func(i int) {

		rec, _ := call(t, env, http.MethodPost, "/api/v1/cart/items",
			`{"variantId":1,"quantity":1}`, token)
		counts.record(rec.Code)

		rec, _ = call(t, env, http.MethodPost, "/api/v1/checkout/complete",
			`{"shippingMethodId":1,"addressId":1,"paymentMethod":"wallet"}`, token)
		counts.record(rec.Code)
	})

	t.Logf("checkout storm: %s", &counts)
	if n := counts.server.Load(); n > 0 {
		t.Errorf("%d requests returned 5xx during the checkout storm", n)
	}

	balance := scalarInt(t, env, `SELECT balance FROM wallets WHERE user_id=7`)
	if balance < 0 {
		t.Errorf("wallet balance is negative after concurrent checkout: %d", balance)
	}

	orphans := scalarInt(t, env, `
		SELECT COUNT(*) FROM orders o
		WHERE o.payment_status='paid'
		  AND NOT EXISTS (SELECT 1 FROM payments p WHERE p.order_id=o.id AND p.status='succeeded')`)
	if orphans > 0 {
		t.Errorf("%d orders are marked paid with no succeeded payment row", orphans)
	}

	debited := scalarInt(t, env, `
		SELECT COALESCE(-SUM(amount),0) FROM wallet_transactions WHERE user_id=7 AND amount < 0`) - debitedBefore
	paid := scalarInt(t, env, `
		SELECT COALESCE(SUM(amount),0) FROM payments
		WHERE user_id=7 AND gateway='wallet' AND status='succeeded'`) - paidBefore

	if debited > funded {
		t.Errorf("wallet was debited %d against funding of %d — it was overdrawn by %d",
			debited, funded, debited-funded)
	}
	if paid > debited {
		t.Errorf("wallet payments total %d but only %d was debited from the wallet", paid, debited)
	}
	if want := funded - debited; balance != want {
		t.Errorf("balance = %d, want %d (funded %d − debited %d)", balance, want, funded, debited)
	}
}

func TestStressIdempotentCheckoutReplay(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	token := bearer(t, env, "zahra@example.com", "Customer@12345")
	mustExec(t, env, `DELETE FROM cart_items WHERE cart_id IN (SELECT id FROM carts WHERE user_id=8)`)
	mustExec(t, env, `UPDATE wallets SET balance=9000000000 WHERE user_id=8`)

	rec, _ := call(t, env, http.MethodPost, "/api/v1/cart/items", `{"variantId":1,"quantity":1}`, token)
	if rec.Code >= 400 {
		t.Fatalf("seed cart: %d %s", rec.Code, rec.Body.String())
	}

	const key = "STRESS-IDEMPOTENT-KEY-0001"
	before := scalarInt(t, env, `SELECT COUNT(*) FROM orders WHERE user_id=8`)

	var served, replayed, inFlight, other atomic.Int32
	hammer(16, func(i int) {
		r := env.Request(http.MethodPost, "/api/v1/checkout/complete",
			`{"shippingMethodId":1,"addressId":2,"paymentMethod":"wallet"}`,
			map[string]string{
				"Authorization":   "Bearer " + token,
				"Content-Type":    "application/json",
				"Idempotency-Key": key,
			})
		switch {
		case r.Result().Header.Get("Idempotent-Replay") == "true":
			replayed.Add(1)
		case r.Code == http.StatusConflict:
			inFlight.Add(1)
		case r.Code == http.StatusCreated:
			served.Add(1)
		default:
			other.Add(1)
		}
		if r.Code >= 500 {
			t.Errorf("idempotent replay returned %d: %s", r.Code, strings.TrimSpace(r.Body.String()))
		}
	})

	after := scalarInt(t, env, `SELECT COUNT(*) FROM orders WHERE user_id=8`)
	t.Logf("served=%d replayed=%d in-flight-409=%d other=%d orders delta=%d",
		served.Load(), replayed.Load(), inFlight.Load(), other.Load(), after-before)

	if after-before != 1 {
		t.Errorf("one Idempotency-Key produced %d orders, want exactly 1", after-before)
	}
	if served.Load() != 1 {
		t.Errorf("%d requests ran the handler, want exactly 1", served.Load())
	}
	if got, want := served.Load()+replayed.Load()+inFlight.Load(), int32(16); got != want {
		t.Errorf("only %d of %d responses were classified", got, want)
	}
	if other.Load() > 0 {
		t.Errorf("%d duplicates returned an unexpected status", other.Load())
	}
}

func TestStressConcurrentCartMutations(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	token := bearer(t, env, "reza@example.com", "Customer@12345")

	var counts statusCounter
	hammer(40, func(i int) {
		switch i % 3 {
		case 0:
			rec, _ := call(t, env, http.MethodPost, "/api/v1/cart/items",
				fmt.Sprintf(`{"variantId":%d,"quantity":1}`, 1+(i%3)), token)
			counts.record(rec.Code)
		case 1:
			rec, _ := call(t, env, http.MethodGet, "/api/v1/cart", "", token)
			counts.record(rec.Code)
		default:
			rec, _ := call(t, env, http.MethodDelete, "/api/v1/cart/items/1", "", token)
			counts.record(rec.Code)
		}
	})

	t.Logf("cart storm: %s", &counts)
	if n := counts.server.Load(); n > 0 {
		t.Errorf("%d cart requests returned 5xx", n)
	}
	if bad := scalarInt(t, env, `SELECT COUNT(*) FROM cart_items WHERE quantity < 0`); bad > 0 {
		t.Errorf("%d cart lines hold a negative quantity", bad)
	}
	if orphan := scalarInt(t, env, `
		SELECT COUNT(*) FROM cart_items ci
		WHERE NOT EXISTS (SELECT 1 FROM carts c WHERE c.id=ci.cart_id)`); orphan > 0 {
		t.Errorf("%d cart lines point at a cart that does not exist", orphan)
	}
}

func TestStressConcurrentRefreshRotation(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())

	cl := env.Browser()
	rec := cl.PostJSON("/api/v1/auth/login", `{"email":"john@example.com","password":"Customer@12345"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	refresh := cl.Cookie("ecms_refresh")
	if refresh == "" {
		t.Skip("no refresh cookie issued; rotation is covered by auth_flows_test.go")
	}

	var accepted, rejected, server atomic.Int32
	hammer(12, func(i int) {
		r := env.Request(http.MethodPost, "/api/v1/auth/refresh-token", "",
			map[string]string{
				"Content-Type": "application/json",
				"Cookie":       "ecms_refresh=" + refresh,
			})
		switch {
		case r.Code >= 500:
			server.Add(1)
		case r.Code == http.StatusOK:
			accepted.Add(1)
		default:
			rejected.Add(1)
		}
	})

	t.Logf("refresh storm: accepted=%d rejected=%d 5xx=%d", accepted.Load(), rejected.Load(), server.Load())
	if server.Load() > 0 {
		t.Errorf("%d refresh attempts returned 5xx", server.Load())
	}
	if accepted.Load() > 1 {
		t.Errorf("a single refresh token was accepted %d times — rotation is not atomic", accepted.Load())
	}
}

func TestStressLoginAttemptsAreThrottled(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())

	var counts statusCounter
	hammer(30, func(i int) {
		r := env.Request(http.MethodPost, "/api/v1/auth/login",
			`{"email":"emma@example.com","password":"definitely-wrong"}`,
			map[string]string{"Content-Type": "application/json"})
		counts.record(r.Code)
	})

	t.Logf("login storm: %s", &counts)
	if n := counts.server.Load(); n > 0 {
		t.Errorf("%d login attempts returned 5xx", n)
	}
	if counts.ok.Load() > 0 {
		t.Errorf("%d wrong-password logins succeeded", counts.ok.Load())
	}
}

func TestStressMixedReadLoad(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	admin := bearer(t, env, "admin@yourstore.com", "Admin@12345")

	public := []string{
		"/api/v1/catalog/products", "/api/v1/blog/posts", "/api/v1/search?q=watch",
		"/api/v1/search/filters", "/api/v1/localization/currencies",
		"/api/v1/localization/languages", "/api/v1/cms/settings", "/api/v1/health",
		"/api/v1/health/db", "/api/v1/health/ready", "/api/v1/shipping/methods",
		"/api/v1/payments/methods", "/api/v1/discounts/flash-sales",
		"/api/v1/feature-flags", "/api/v1/_errors", "/api/v1/_routes",
	}
	adminOnly := []string{
		"/api/v1/admin/stats/overview", "/api/v1/admin/reports/revenue",
		"/api/v1/admin/reports/top-products", "/api/v1/admin/cron/jobs",
		"/api/v1/admin/queues", "/api/v1/admin/logs/audit", "/api/v1/admin/orders",
		"/api/v1/admin/users", "/api/v1/admin/system/info",
	}

	var counts statusCounter
	var failures sync.Map

	hammer(len(public)*3+len(adminOnly)*3, func(i int) {
		var path, token string
		if i%2 == 0 {
			path, token = public[i/2%len(public)], ""
		} else {
			path, token = adminOnly[i/2%len(adminOnly)], admin
		}
		r := env.Request(http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + token})
		counts.record(r.Code)

		if r.Code >= 500 || r.Code == http.StatusNotFound {
			failures.Store(path, r.Code)
		}
	})

	t.Logf("mixed read load: %s", &counts)
	failures.Range(func(k, v any) bool {
		t.Errorf("%v returned %v under load", k, v)
		return true
	})
}

func TestStressSettingsWriteDuringReadLoad(t *testing.T) {
	env := testsupport.New(t, testsupport.NoRateLimit())
	admin := bearer(t, env, "admin@yourstore.com", "Admin@12345")

	mustExec(t, env,
		`INSERT INTO settings(key,value,scope) VALUES('stress_private_token','s3cr3t','private')
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, scope='private'`)

	var counts statusCounter
	hammer(45, func(i int) {
		switch i % 3 {
		case 0:
			rec, _ := call(t, env, http.MethodPatch, "/api/v1/cms/settings",
				fmt.Sprintf(`{"stress_private_token":"rotated-%d"}`, i), admin)
			counts.record(rec.Code)
		case 1:
			rec, _ := call(t, env, http.MethodGet, "/api/v1/cms/settings", "", "")
			counts.record(rec.Code)
		default:
			rec, _ := call(t, env, http.MethodGet, "/api/v1/health", "", "")
			counts.record(rec.Code)
		}
	})

	t.Logf("settings storm: %s", &counts)
	if n := counts.server.Load(); n > 0 {
		t.Errorf("%d requests returned 5xx while settings were being written", n)
	}

	if scope := scalar(t, env, `SELECT scope FROM settings WHERE key='stress_private_token'`); scope != "private" {
		t.Errorf("a value update re-scoped the setting to %q", scope)
	}

	rec, body := call(t, env, http.MethodGet, "/api/v1/cms/settings", "", "")
	if rec.Code == http.StatusOK {
		var payload map[string]any
		if err := json.Unmarshal(body.Data, &payload); err == nil {
			for k := range payload {
				if strings.HasPrefix(k, "stress_private") {
					t.Errorf("private setting %q leaked into the public settings payload", k)
				}
			}
		}
	}
}

func TestStressRateLimiterUnderBurst(t *testing.T) {
	env := testsupport.New(t)

	var limited, served, server atomic.Int32
	hammer(150, func(i int) {
		r := env.Request(http.MethodGet, "/api/v1/catalog/products", "", nil)
		switch {
		case r.Code >= 500:
			server.Add(1)
		case r.Code == http.StatusTooManyRequests:
			limited.Add(1)
		default:
			served.Add(1)
		}
	})

	t.Logf("limiter burst: served=%d limited=%d 5xx=%d", served.Load(), limited.Load(), server.Load())
	if server.Load() > 0 {
		t.Errorf("%d requests returned 5xx under the rate limiter", server.Load())
	}
	if served.Load() == 0 {
		t.Error("the limiter rejected every request in the burst")
	}
}
