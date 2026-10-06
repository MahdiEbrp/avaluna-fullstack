package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBothLocalesHaveTheSameKeys(t *testing.T) {
	fa := Keys("fa")
	en := Keys("en")
	if len(fa) == 0 || len(en) == 0 {
		t.Fatal("catalogues must not be empty")
	}
	for k := range fa {
		if _, ok := en[k]; !ok {
			t.Errorf("key %q is missing from the English catalogue", k)
		}
	}
	for k := range en {
		if _, ok := fa[k]; !ok {
			t.Errorf("key %q is missing from the Persian catalogue", k)
		}
	}
}

func TestNoEmptyTranslations(t *testing.T) {
	for _, loc := range []string{"fa", "en"} {
		for k, v := range Keys(loc) {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s/%s is empty", loc, k)
			}
		}
	}
}

func TestTranslationFallsBackToTheKey(t *testing.T) {
	if got := T("en", "totally.unknown.key"); got != "totally.unknown.key" {
		t.Errorf("unknown key returned %q", got)
	}
	if T("en", "nav.shop") == "nav.shop" {
		t.Error("a known key must be translated")
	}
}

func TestDirectionAndRTL(t *testing.T) {
	if Dir("fa") != "rtl" || !IsRTL("fa") {
		t.Error("Persian must be right-to-left")
	}
	if Dir("en") != "ltr" || IsRTL("en") {
		t.Error("English must be left-to-right")
	}

	if Dir("zz") != Dir(Default) {
		t.Errorf("unknown locale direction = %q, want the default %q", Dir("zz"), Dir(Default))
	}
}

func TestResolveOrdersCookieThenHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	if got := Resolve(r); got != "en" {
		t.Errorf("Accept-Language resolution = %q", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("Accept-Language", "en-GB,en;q=0.9")
	r2.AddCookie(&http.Cookie{Name: CookieName, Value: "fa"})
	if got := Resolve(r2); got != "fa" {
		t.Errorf("the cookie must win over Accept-Language, got %q", got)
	}

	r3 := httptest.NewRequest(http.MethodGet, "/?lang=en", nil)
	r3.AddCookie(&http.Cookie{Name: CookieName, Value: "fa"})
	if got := Resolve(r3); got != "en" {
		t.Errorf("an explicit ?lang must win, got %q", got)
	}
}

func TestPersianDigitsAndGrouping(t *testing.T) {
	if got := ToPersianDigits("2026"); got != "۲۰۲۶" {
		t.Errorf("ToPersianDigits = %q", got)
	}
	if got := Group(1234567); got != "1,234,567" {
		t.Errorf("Group = %q", got)
	}
	if got := Num("en", 1234567); got != "1,234,567" {
		t.Errorf("Num(en) = %q", got)
	}
	if got := Num("fa", 1234); !strings.Contains(got, "۱") {
		t.Errorf("Num(fa) should use Persian digits, got %q", got)
	}
}

func TestMoneyFormatting(t *testing.T) {

	fa := Money("fa", "IRR", 920_000_000)
	if !strings.Contains(fa, "تومان") {
		t.Errorf("Persian IRR formatting = %q, want Toman suffix", fa)
	}
	if want := ToPersianDigits("92,000,000") + " تومان"; fa != want {
		t.Errorf("Money(fa, IRR, 920000000) = %q, want %q", fa, want)
	}
	if got, want := Money("en", "IRR", 920_000_000), "920,000,000 IRR"; got != want {
		t.Errorf("Money(en, IRR, 920000000) = %q, want %q", got, want)
	}
	en := Money("en", "USD", 1999)
	if !strings.Contains(en, "19.99") && !strings.Contains(en, "1,999") {
		t.Errorf("USD formatting = %q", en)
	}
	if Money("en", "IRR", 0) == "" {
		t.Error("zero amounts must still format")
	}
}

func TestTranslatorHelper(t *testing.T) {
	tr := Translator{Loc: "fa"}
	if !tr.RTL() || tr.Dir() != "rtl" {
		t.Error("Persian translator should report RTL")
	}
	if tr.T("nav.shop") == "" {
		t.Error("translator lookup failed")
	}
}
