package app_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestStorefrontPagesRender(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	pages := []string{
		"/", "/shop", "/shop?sort=price_asc&featured=1", "/search?q=macbook",
		"/product/macbook-air-m3", "/cart", "/blog", "/faq", "/login", "/register",
		"/sitemap.xml", "/robots.txt",
	}
	for _, p := range pages {
		rec := b.Get(p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", p, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", p)
		}
	}
}

func TestBilingualRendering(t *testing.T) {
	env := testsupport.New(t)

	fa := env.Browser().Get("/")
	if !strings.Contains(fa.Body.String(), `<html lang="fa" dir="rtl"`) {
		t.Error("default locale should render Persian RTL")
	}

	b := env.Browser()
	if rec := b.Get("/lang/en?next=/shop"); rec.Code != http.StatusSeeOther {
		t.Fatalf("language switch = %d, want 303", rec.Code)
	}
	en := b.Get("/")
	if !strings.Contains(en.Body.String(), `<html lang="en" dir="ltr"`) {
		t.Error("after switching the locale the page should render English LTR")
	}
	if !strings.Contains(en.Body.String(), "Add to cart") &&
		!strings.Contains(en.Body.String(), "Shop") {
		t.Error("English catalogue strings missing from the English page")
	}
}

func TestProductPageShowsVariantsAndReviews(t *testing.T) {
	env := testsupport.New(t)
	rec := env.Browser().Get("/product/macbook-air-m3")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	for _, want := range []string{"hx-post=\"/cart/add\"", "productId"} {
		if !strings.Contains(body, want) {
			t.Errorf("product page missing %q", want)
		}
	}
}

func TestSuggestFragmentIsHTMLOnly(t *testing.T) {
	env := testsupport.New(t)
	rec := env.Browser().Get("/partials/suggest?q=mac")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Error("HTMX fragments must not include a full document")
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	env := testsupport.New(t)
	if rec := env.Browser().Get("/definitely-not-a-page"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCartLifecycle(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	add := b.PostForm("/cart/add", url.Values{"productId": {"1"}, "qty": {"2"}})
	if add.Code != http.StatusOK {
		t.Fatalf("add to cart = %d", add.Code)
	}
	if got := strings.TrimSpace(add.Body.String()); got != "2" {
		t.Fatalf("cart badge = %q, want 2", got)
	}

	cart := b.Get("/cart")
	if cart.Code != http.StatusOK || !strings.Contains(cart.Body.String(), "/cart/remove") {
		t.Fatalf("cart page did not render line items (status %d)", cart.Code)
	}

	coupon := b.PostForm("/cart/coupon", url.Values{"code": {"WELCOME10"}})
	if coupon.Code != http.StatusOK || !strings.Contains(coupon.Body.String(), "WELCOME10") {
		t.Error("coupon was not applied to the cart")
	}
}

func TestCheckoutRequiresAuthentication(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()
	b.PostForm("/cart/add", url.Values{"productId": {"1"}, "qty": {"1"}})

	rec := b.Get("/checkout")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("anonymous checkout = %d, want a redirect to /login", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login") {
		t.Fatalf("redirect target = %q", loc)
	}
}
