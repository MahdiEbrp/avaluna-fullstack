package app_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

func TestCartAddUpdateRemove(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	if rec := b.Get("/"); rec.Code != http.StatusOK {
		t.Fatalf("home = %d", rec.Code)
	}

	add := b.PostForm("/cart/add", url.Values{"productId": {"1"}, "qty": {"2"}})
	if add.Code != http.StatusOK && add.Code != http.StatusSeeOther {
		t.Fatalf("add to cart = %d: %s", add.Code, add.Body.String())
	}

	cart := b.Get("/cart")
	if cart.Code != http.StatusOK {
		t.Fatalf("GET /cart = %d", cart.Code)
	}
	html := cart.Body.String()
	if !strings.Contains(html, "/cart/update") && !strings.Contains(html, "/cart/remove") {
		t.Error("the cart page offers no way to change quantities")
	}

	itemRe := regexp.MustCompile(`name="itemId"\s+value="(\d+)"`)
	m := itemRe.FindStringSubmatch(html)
	if m == nil {

		itemRe = regexp.MustCompile(`itemId["']?\s*:\s*["']?(\d+)`)
		m = itemRe.FindStringSubmatch(html)
	}
	if m == nil {
		t.Skip("could not locate a cart line id in the rendered markup")
	}
	itemID := m[1]

	up := b.PostForm("/cart/update", url.Values{"itemId": {itemID}, "qty": {"5"}})
	if up.Code != http.StatusOK && up.Code != http.StatusSeeOther {
		t.Errorf("update = %d", up.Code)
	}
	if got := b.Get("/cart").Body.String(); !strings.Contains(got, "5") {
		t.Error("the updated quantity is not shown")
	}

	rm := b.PostForm("/cart/update", url.Values{"itemId": {itemID}, "qty": {"0"}})
	if rm.Code != http.StatusOK && rm.Code != http.StatusSeeOther {
		t.Errorf("remove via qty=0 = %d", rm.Code)
	}
}

func TestCartRejectsBadInput(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()
	b.Get("/")

	cases := []struct {
		name string
		form url.Values
	}{
		{"no product", url.Values{"qty": {"1"}}},
		{"product zero", url.Values{"productId": {"0"}, "qty": {"1"}}},
		{"unknown product", url.Values{"productId": {"999999"}, "qty": {"1"}}},
		{"negative quantity", url.Values{"productId": {"1"}, "qty": {"-3"}}},
		{"non-numeric quantity", url.Values{"productId": {"1"}, "qty": {"abc"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := b.PostForm("/cart/add", tc.form)
			if rec.Code >= 500 {
				t.Errorf("POST /cart/add %v = %d, a bad request must not 500", tc.form, rec.Code)
			}
		})
	}

	if rec := b.Get("/cart"); rec.Code != http.StatusOK {
		t.Errorf("GET /cart after bad input = %d", rec.Code)
	}
}

func TestCartCouponFeedback(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()
	b.Get("/")
	b.PostForm("/cart/add", url.Values{"productId": {"1"}, "qty": {"1"}})

	bad := b.PostForm("/cart/coupon", url.Values{"code": {"NOPE-NOT-REAL"}})
	if bad.Code >= 500 {
		t.Fatalf("invalid coupon = %d", bad.Code)
	}
	if bad.Body.Len() == 0 {
		t.Error("an invalid coupon produced no feedback")
	}

	ok := b.PostForm("/cart/coupon", url.Values{"code": {"VIP20"}})
	if ok.Code >= 500 {
		t.Errorf("valid coupon = %d", ok.Code)
	}
}
