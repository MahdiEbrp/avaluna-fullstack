package i18n

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type Locale struct {
	Code      string
	Name      string
	NativeTag string
	Dir       string
	Font      string
	Currency  string
}

var Locales = map[string]Locale{
	"fa": {Code: "fa", Name: "فارسی", NativeTag: "fa-IR", Dir: "rtl", Font: "Vazirmatn", Currency: "IRR"},
	"en": {Code: "en", Name: "English", NativeTag: "en-US", Dir: "ltr", Font: "Inter", Currency: "USD"},
}

var Order = []string{"fa", "en"}

const CookieName = "ecms_locale"

const Default = "fa"

func Explicit(r *http.Request) string {
	if q := strings.ToLower(r.URL.Query().Get("lang")); valid(q) {
		return q
	}
	if c, err := r.Cookie(CookieName); err == nil && valid(c.Value) {
		return c.Value
	}
	return ""
}

func Resolve(r *http.Request) string {
	if q := strings.ToLower(r.URL.Query().Get("lang")); valid(q) {
		return q
	}
	if c, err := r.Cookie(CookieName); err == nil && valid(c.Value) {
		return c.Value
	}
	al := strings.ToLower(r.Header.Get("Accept-Language"))
	for _, part := range strings.Split(al, ",") {
		tag := strings.TrimSpace(strings.Split(part, ";")[0])
		if len(tag) >= 2 && valid(tag[:2]) {
			return tag[:2]
		}
	}
	return Default
}

func valid(c string) bool { _, ok := Locales[c]; return ok }

func Dir(loc string) string {
	if l, ok := Locales[loc]; ok {
		return l.Dir
	}
	return "rtl"
}

func IsRTL(loc string) bool { return Dir(loc) == "rtl" }

func T(loc, key string, args ...any) string {
	table, ok := catalog[loc]
	if !ok {
		table = catalog[Default]
	}
	s, ok := table[key]
	if !ok {
		if s, ok = catalog["en"][key]; !ok {
			s = key
		}
	}
	if len(args) > 0 {
		return fmt.Sprintf(s, args...)
	}
	return s
}

type Translator struct{ Loc string }

func (t Translator) T(key string, args ...any) string { return T(t.Loc, key, args...) }

func (t Translator) Dir() string { return Dir(t.Loc) }

func (t Translator) RTL() bool { return IsRTL(t.Loc) }

var faDigits = []rune("۰۱۲۳۴۵۶۷۸۹")

func ToPersianDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(faDigits[r-'0'])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func Group(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}

func Money(loc, currency string, amount int64) string {
	switch strings.ToUpper(currency) {
	case "IRR":
		if loc == "fa" {
			return ToPersianDigits(Group(amount/10)) + " تومان"
		}
		return Group(amount) + " IRR"
	case "IRT":
		if loc == "fa" {
			return ToPersianDigits(Group(amount)) + " تومان"
		}
		return Group(amount) + " Toman"
	case "USD":
		return "$" + Group(amount/100) + "." + fmt.Sprintf("%02d", amount%100)
	case "EUR":
		return "€" + Group(amount/100) + "." + fmt.Sprintf("%02d", amount%100)
	default:
		return Group(amount) + " " + currency
	}
}

func Num(loc string, n int64) string {
	s := Group(n)
	if loc == "fa" {
		return ToPersianDigits(s)
	}
	return s
}

func Keys(loc string) map[string]string {
	table, ok := catalog[loc]
	if !ok {
		table = catalog[Default]
	}
	out := make(map[string]string, len(table))
	for k, v := range table {
		out[k] = v
	}
	return out
}
