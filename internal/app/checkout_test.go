package app_test

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/testsupport"
)

const (
	customerEmail = "ali@example.com"
	customerPass  = "Customer@12345"
	adminEmail    = "admin@yourstore.com"
	adminPass     = "Admin@12345"
)

func login(t *testing.T, env *testsupport.Env, email, pass string) *testsupport.Client {
	t.Helper()
	b := env.Browser()
	rec := b.PostForm("/login", url.Values{"email": {email}, "password": {pass}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d, want 303 (body: %.200s)", email, rec.Code, rec.Body.String())
	}
	return b
}

var refRe = regexp.MustCompile(`PAY-[A-Z0-9]+`)

func TestFullPurchaseFlowWithSandboxGateway(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, customerEmail, customerPass)

	if rec := b.PostForm("/cart/add", url.Values{"productId": {"2"}, "qty": {"1"}}); rec.Code != 200 {
		t.Fatalf("add to cart = %d", rec.Code)
	}
	if rec := b.Get("/checkout"); rec.Code != http.StatusOK {
		t.Fatalf("checkout page = %d", rec.Code)
	}

	form := url.Values{
		"shippingMethodId": {"1"}, "gateway": {"zarinpal"},
		"recipient": {"Ali Rezaei"}, "phone": {"09120000000"},
		"province": {"Tehran"}, "city": {"Tehran"},
		"postalCode": {"1234567890"}, "line1": {"Valiasr St, No. 1"},
	}
	rec := b.PostForm("/checkout", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("checkout submit = %d, want 303 (body: %.400s)", rec.Code, rec.Body.String())
	}
	orderPath := rec.Header().Get("Location")
	if !strings.HasPrefix(orderPath, "/order/ORD-") {
		t.Fatalf("unexpected order redirect %q", orderPath)
	}

	page := b.Get(orderPath)
	if page.Code != http.StatusOK {
		t.Fatalf("order page = %d", page.Code)
	}
	ref := refRe.FindString(page.Body.String())
	if ref == "" {
		t.Fatal("sandbox payment reference missing from the confirmation page")
	}

	pay := b.PostForm("/pay/simulate", url.Values{"reference": {ref}, "outcome": {"success"}})
	if pay.Code != http.StatusOK || !strings.Contains(pay.Body.String(), "succeeded") {
		t.Fatalf("payment simulation failed: %d %s", pay.Code, pay.Body.String())
	}

	number := strings.TrimPrefix(orderPath, "/order/")
	var status, paymentStatus string
	err := env.DB.Row(context.Background(),
		`SELECT status, payment_status FROM orders WHERE number=?`, []any{number}, &status, &paymentStatus)
	if err != nil {
		t.Fatalf("read order: %v", err)
	}
	if paymentStatus != "paid" {
		t.Errorf("payment_status = %q, want paid", paymentStatus)
	}

	if cart := b.Get("/cart"); strings.Contains(cart.Body.String(), "/cart/remove") {
		t.Error("cart should be empty after a successful checkout")
	}
}

func TestWalletCheckoutSettlesImmediately(t *testing.T) {
	env := testsupport.New(t)
	ctx := context.Background()
	var uid int
	if err := env.DB.Row(ctx, `SELECT id FROM users WHERE email=?`, []any{customerEmail}, &uid); err != nil {
		t.Fatalf("user lookup: %v", err)
	}
	if _, err := api.WalletCredit(ctx, env.Core, uid, 99_000_000_000, "topup", "TEST-TOPUP"); err != nil {
		t.Fatalf("top up: %v", err)
	}

	b := login(t, env, customerEmail, customerPass)
	b.PostForm("/cart/add", url.Values{"productId": {"6"}, "qty": {"1"}})
	rec := b.PostForm("/checkout", url.Values{
		"shippingMethodId": {"1"}, "gateway": {"wallet"},
		"recipient": {"Ali"}, "phone": {"09120000000"}, "province": {"Tehran"},
		"city": {"Tehran"}, "postalCode": {"1234567890"}, "line1": {"Somewhere 2"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("wallet checkout = %d (%.300s)", rec.Code, rec.Body.String())
	}
	number := strings.TrimPrefix(rec.Header().Get("Location"), "/order/")
	var paymentStatus string
	if err := env.DB.Row(ctx, `SELECT payment_status FROM orders WHERE number=?`,
		[]any{number}, &paymentStatus); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if paymentStatus != "paid" {
		t.Errorf("wallet order payment_status = %q, want paid", paymentStatus)
	}
}

func TestAccountTabsRender(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, customerEmail, customerPass)
	tabs := []string{"dashboard", "orders", "wallet", "loyalty", "wishlist", "addresses",
		"returns", "subscriptions", "giftcards", "referral", "notifications"}
	for _, tab := range tabs {
		rec := b.Get("/account/" + tab)
		if rec.Code != http.StatusOK {
			t.Errorf("GET /account/%s = %d", tab, rec.Code)
		}
	}
}

func TestWishlistAndCompare(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, customerEmail, customerPass)

	if rec := b.Do(http.MethodPost, "/account/wishlist/4", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("wishlist add = %d", rec.Code)
	}
	if rec := b.Get("/account/wishlist"); !strings.Contains(rec.Body.String(), "/product/") {
		t.Error("wishlist page does not list the saved product")
	}
	if rec := b.Do(http.MethodPost, "/compare/4", nil, ""); rec.Code != http.StatusOK {
		t.Fatalf("compare add = %d", rec.Code)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	env := testsupport.New(t)
	rec := env.Browser().PostForm("/login",
		url.Values{"email": {customerEmail}, "password": {"wrong-password"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the form re-rendered with 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "form") {
		t.Error("the login form should be re-rendered with an error")
	}
}

func TestCouponFeedbackDistinguishesSuccessFromRejection(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, customerEmail, customerPass)

	if rec := b.PostForm("/cart/add", url.Values{"productId": {"1"}, "qty": {"1"}}); rec.Code != 200 {
		t.Fatalf("add to cart = %d", rec.Code)
	}

	cases := []struct {
		name    string
		code    string
		wantMsg string
		applied bool
	}{
		{"unknown code", "NO-SUCH-CODE", "coupon not found", false},
		{"expired code", "EXPIRED5", "coupon has expired", false},

		{"already redeemed", "WELCOME10", "already used", false},
		{"valid code", "VIP20", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := b.PostForm("/cart/coupon", url.Values{"code": {tc.code}})
			if rec.Code != http.StatusOK {
				t.Fatalf("apply %s = %d", tc.code, rec.Code)
			}
			body := rec.Body.String()
			if tc.applied {
				if !strings.Contains(body, "text-emerald-600\">Coupon applied.") &&
					!strings.Contains(body, "کد تخفیف اعمال شد.") {
					t.Errorf("%s: expected a success confirmation, got none", tc.code)
				}
				if strings.Contains(body, "text-rose-600\">percent") {
					t.Errorf("%s: internal coupon kind leaked into the UI as an error", tc.code)
				}
				return
			}
			if !strings.Contains(body, tc.wantMsg) {
				t.Errorf("%s: want rejection reason %q in page", tc.code, tc.wantMsg)
			}
			if !strings.Contains(body, "text-rose-600") {
				t.Errorf("%s: rejection must be styled as an error", tc.code)
			}
		})
	}
}

func TestLocaleCookieOverridesTokenClaim(t *testing.T) {
	env := testsupport.New(t)
	b := login(t, env, adminEmail, adminPass)

	if rec := b.Get("/lang/en"); rec.Code != http.StatusSeeOther {
		t.Fatalf("language switch = %d", rec.Code)
	}
	rec := b.Get("/")
	if !strings.Contains(rec.Body.String(), `<html lang="en" dir="ltr"`) {
		t.Error("signed-in user switching to English still got the Persian layout")
	}
	if rec := b.Get("/admin"); !strings.Contains(rec.Body.String(), "Dashboard") {
		t.Error("admin shell did not follow the English switch")
	}

	if rec := b.Get("/lang/fa"); rec.Code != http.StatusSeeOther {
		t.Fatalf("language switch back = %d", rec.Code)
	}
	if rec := b.Get("/"); !strings.Contains(rec.Body.String(), `<html lang="fa" dir="rtl"`) {
		t.Error("switching back to Persian did not restore the RTL layout")
	}
}
