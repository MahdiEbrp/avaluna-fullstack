package app_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

var productHref = regexp.MustCompile(`/product/([a-z0-9\-]+)`)

func listedProducts(html string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range productHref.FindAllStringSubmatch(html, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func TestShopFiltersNarrowTheResults(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	all := b.Get("/shop?limit=100")
	if all.Code != http.StatusOK {
		t.Fatalf("GET /shop = %d", all.Code)
	}
	baseline := len(listedProducts(all.Body.String()))
	if baseline < 3 {
		t.Fatalf("the seed listed only %d products; filters cannot be tested", baseline)
	}

	cases := []struct{ name, query string }{
		{"keyword", "/shop?q=macbook"},
		{"category", "/shop?category=laptops"},
		{"brand", "/shop?brand=apple"},
		{"min price", "/shop?minPrice=500000000"},
		{"max price", "/shop?maxPrice=60000000"},
		{"price window", "/shop?minPrice=50000000&maxPrice=200000000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := b.Get(tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d", tc.query, rec.Code)
			}
			got := len(listedProducts(rec.Body.String()))
			if got >= baseline {
				t.Errorf("%s returned %d of %d products — the filter did nothing",
					tc.query, got, baseline)
			}
		})
	}
}

func TestShopFiltersCombineAndSurviveNonsense(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	one := b.Get("/shop?category=laptops&limit=100")
	both := b.Get("/shop?category=laptops&brand=apple&limit=100")
	if both.Code != http.StatusOK {
		t.Fatalf("combined filter = %d", both.Code)
	}
	if len(listedProducts(both.Body.String())) > len(listedProducts(one.Body.String())) {
		t.Error("adding a brand filter returned more products than the category alone")
	}

	for _, q := range []string{
		"/shop?category=does-not-exist",
		"/shop?brand=nope",
		"/shop?minPrice=abc&maxPrice=xyz",
		"/shop?minPrice=999999999999",
		"/shop?maxPrice=1",
		"/shop?page=-5",
		"/shop?page=99999",
		"/shop?sort=%27+OR+1%3D1--",
		"/shop?q=%27%29+DROP+TABLE+products%3B--",
	} {
		rec := b.Get(q)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want a rendered page", q, rec.Code)
		}
	}
}

func TestShopSortOrdersByPrice(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	for _, s := range []string{"newest", "price_asc", "price_desc", "popular", "rating"} {
		rec := b.Get("/shop?sort=" + s + "&limit=100")
		if rec.Code != http.StatusOK {
			t.Errorf("sort=%s returned %d", s, rec.Code)
		}
		if len(listedProducts(rec.Body.String())) == 0 {
			t.Errorf("sort=%s listed no products", s)
		}
	}

	cheapest, err := env.DB.Scalar(context.Background(),
		`SELECT slug FROM products WHERE status='published' ORDER BY price ASC LIMIT 1`)
	if err != nil {
		t.Fatalf("cheapest: %v", err)
	}
	dearest, err := env.DB.Scalar(context.Background(),
		`SELECT slug FROM products WHERE status='published' ORDER BY price DESC LIMIT 1`)
	if err != nil {
		t.Fatalf("dearest: %v", err)
	}

	asc := listedProducts(b.Get("/shop?sort=price_asc&limit=100").Body.String())
	desc := listedProducts(b.Get("/shop?sort=price_desc&limit=100").Body.String())
	if len(asc) == 0 || len(desc) == 0 {
		t.Fatal("a sorted listing was empty")
	}
	if asc[0] != cheapest.String {
		t.Errorf("price_asc leads with %q, want the cheapest product %q", asc[0], cheapest.String)
	}
	if desc[0] != dearest.String {
		t.Errorf("price_desc leads with %q, want the dearest product %q", desc[0], dearest.String)
	}
}

func TestShopPaginationAdvances(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	p1 := b.Get("/shop?limit=4&page=1")
	p2 := b.Get("/shop?limit=4&page=2")
	if p1.Code != http.StatusOK || p2.Code != http.StatusOK {
		t.Fatalf("pagination returned %d / %d", p1.Code, p2.Code)
	}
	first := listedProducts(p1.Body.String())
	second := listedProducts(p2.Body.String())
	if len(first) == 0 || len(second) == 0 {
		t.Fatal("a pagination page was empty")
	}

	overlap := 0
	for _, a := range first {
		for _, bb := range second {
			if a == bb {
				overlap++
			}
		}
	}
	if overlap == len(first) {
		t.Errorf("page 2 repeated page 1 exactly: %v", first)
	}
}

func TestSearchSuggestions(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	short := b.Get("/partials/suggest?q=m")
	if short.Code != http.StatusOK {
		t.Errorf("one-character suggest = %d", short.Code)
	}
	if n := len(listedProducts(short.Body.String())); n > 0 {
		t.Errorf("a one-character query returned %d suggestions, want none", n)
	}

	hit := b.Get("/partials/suggest?q=macbook")
	if hit.Code != http.StatusOK {
		t.Fatalf("suggest = %d", hit.Code)
	}
	if !strings.Contains(strings.ToLower(hit.Body.String()), "macbook") {
		t.Error("a matching query produced no suggestion")
	}

	miss := b.Get("/partials/suggest?q=zzzzzznotathing")
	if miss.Code != http.StatusOK {
		t.Errorf("no-match suggest = %d", miss.Code)
	}
}

func TestProductPageHandlesMissingSlug(t *testing.T) {
	env := testsupport.New(t)
	b := env.Browser()

	rec := b.Get("/product/no-such-product-slug")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown product = %d, want 404", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("the 404 page was empty")
	}
}
