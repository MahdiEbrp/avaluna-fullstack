package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/middleware"
)

func TestSlugify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"  Trimmed  ", "trimmed"},
		{"MacBook Air M3", "macbook-air-m3"},
		{"multiple   spaces", "multiple-spaces"},
		{"Punctuation!@#$%^&*()", "punctuation"},
		{"already-a-slug", "already-a-slug"},
		{"--leading-and-trailing--", "leading-and-trailing"},
		{"CAPS_AND_UNDERSCORES", "caps-and-underscores"},
		{"mixed 123 numbers", "mixed-123-numbers"},
		{"", ""},
		{"---", ""},

		{"لپ تاپ اپل", "لپ-تاپ-اپل"},
		{"کیبورد Logitech", "کیبورد-logitech"},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyIsIdempotent(t *testing.T) {
	for _, in := range []string{"Hello World", "لپ تاپ", "a--b", "Ünïcödé Tëxt"} {
		once := Slugify(in)
		if twice := Slugify(once); twice != once {
			t.Errorf("Slugify not idempotent for %q: %q then %q", in, once, twice)
		}
	}
}

func TestCoerceInt(t *testing.T) {
	f := Field{Name: "n", Kind: KindInt}
	ok := []struct {
		raw  any
		want int64
	}{
		{float64(42), 42},
		{float64(-7), -7},
		{42, 42},
		{int64(42), 42},
		{"42", 42},
		{"  42  ", 42},
		{"", 0},
		{true, 1},
		{false, 0},
	}
	for _, c := range ok {
		got, err := coerce(f, c.raw)
		if err != nil {
			t.Errorf("coerce(%v) errored: %v", c.raw, err)
			continue
		}
		if got != any(c.want) {
			t.Errorf("coerce(%#v) = %#v, want %d", c.raw, got, c.want)
		}
	}
	for _, bad := range []any{"abc", "1.5", nil, []any{1}, map[string]any{}} {
		if _, err := coerce(f, bad); err == nil {
			t.Errorf("coerce(%#v) should have failed", bad)
		}
	}
}

func TestCoerceFloat(t *testing.T) {
	f := Field{Name: "x", Kind: KindFloat}
	for raw, want := range map[any]float64{
		float64(1.5): 1.5,
		"2.25":       2.25,
		"  3  ":      3,
		"":           0,
	} {
		got, err := coerce(f, raw)
		if err != nil {
			t.Errorf("coerce(%v): %v", raw, err)
			continue
		}
		if got != any(want) {
			t.Errorf("coerce(%#v) = %#v, want %v", raw, got, want)
		}
	}
	for _, bad := range []any{"abc", nil, true} {
		if _, err := coerce(f, bad); err == nil {
			t.Errorf("coerce(%#v) should have failed", bad)
		}
	}
}

func TestCoerceBoolAcceptsTheUsualSpellings(t *testing.T) {
	f := Field{Name: "b", Kind: KindBool}
	truthy := []any{true, float64(1), float64(-1), "1", "true", "TRUE", " yes ", "on", "On"}
	for _, raw := range truthy {
		got, err := coerce(f, raw)
		if err != nil || got != any(int64(1)) {
			t.Errorf("coerce(%#v) = %#v (err %v), want 1", raw, got, err)
		}
	}
	falsy := []any{false, float64(0), "0", "false", "no", "off", "", "nonsense", nil}
	for _, raw := range falsy {
		got, err := coerce(f, raw)
		if err != nil || got != any(int64(0)) {
			t.Errorf("coerce(%#v) = %#v (err %v), want 0", raw, got, err)
		}
	}
}

func TestCoerceSlugAndJSON(t *testing.T) {
	slug, err := coerce(Field{Kind: KindSlug}, "My New Post")
	if err != nil || slug != any("my-new-post") {
		t.Errorf("slug coercion = %#v (%v)", slug, err)
	}

	empty, err := coerce(Field{Kind: KindJSON}, nil)
	if err != nil || empty != any("{}") {
		t.Errorf("empty JSON coercion = %#v (%v), want {}", empty, err)
	}
	passthrough, _ := coerce(Field{Kind: KindJSON}, `{"a":1}`)
	if passthrough != any(`{"a":1}`) {
		t.Errorf("JSON passthrough = %#v", passthrough)
	}
	str, _ := coerce(Field{Kind: KindString}, float64(12))
	if str != any("12") {
		t.Errorf("string coercion of a whole float = %#v, want \"12\"", str)
	}
}

func TestToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""},
		{"text", "text"},
		{float64(12), "12"},
		{float64(1.25), "1.25"},
		{float64(-3), "-3"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
	}
	for _, c := range cases {
		if got := toString(c.in); got != c.want {
			t.Errorf("toString(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIsEmptyAndContains(t *testing.T) {
	for _, s := range []any{"", "   ", "\t\n"} {
		if !isEmpty(s) {
			t.Errorf("isEmpty(%q) = false", s)
		}
	}
	for _, s := range []any{"x", " x ", 0, nil, false} {
		if isEmpty(s) {
			t.Errorf("isEmpty(%#v) = true", s)
		}
	}
	list := []string{"a", "b", "c"}
	if !contains(list, "b") || contains(list, "z") || contains(nil, "a") {
		t.Error("contains behaved unexpectedly")
	}
}

func TestConflictOrMapsConstraintViolations(t *testing.T) {
	cases := map[string]string{
		"UNIQUE constraint failed: products.slug": "CONFLICT",
		"constraint failed":                       "CONFLICT",
		"FOREIGN KEY constraint failed":           "CONFLICT",
		"disk I/O error":                          "INTERNAL_SERVER_ERROR",
		"no such table":                           "INTERNAL_SERVER_ERROR",
	}
	for msg, want := range cases {
		if got := conflictOr(errString(msg)); got != want {
			t.Errorf("conflictOr(%q) = %s, want %s", msg, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestResourceDefaults(t *testing.T) {
	bare := &Resource{Name: "things", Table: "things"}
	if got := bare.selectList(); got != "*" {
		t.Errorf("default select = %q, want *", got)
	}
	if got := bare.order(); got != "id DESC" {
		t.Errorf("default order = %q, want id DESC", got)
	}
	custom := &Resource{Select: "id,name", OrderBy: "name ASC"}
	if custom.selectList() != "id,name" || custom.order() != "name ASC" {
		t.Error("custom select/order were not honoured")
	}
}

func TestLookupColumnPrefersSlugThenCodeThenKey(t *testing.T) {
	cases := []struct {
		fields []Field
		want   string
	}{
		{[]Field{{Name: "slug"}, {Name: "code"}, {Name: "key"}}, "slug"},
		{[]Field{{Name: "code"}, {Name: "key"}}, "code"},
		{[]Field{{Name: "key"}}, "key"},
		{[]Field{{Name: "name"}}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		rs := &Resource{Fields: c.fields}
		if got := rs.lookupColumn(); got != c.want {
			t.Errorf("lookupColumn(%v) = %q, want %q", c.fields, got, c.want)
		}
	}
}

func TestSortClauseRejectsUnknownColumns(t *testing.T) {
	rs := &Resource{
		Fields:  []Field{{Name: "name"}, {Name: "price"}},
		Filters: []string{"status"},
	}
	cases := map[string]string{
		"name":        "name ASC",
		"-name":       "name DESC",
		"price":       "price ASC",
		"-price":      "price DESC",
		"status":      "status ASC",
		"id":          "id ASC",
		"-created_at": "created_at DESC",
		"updated_at":  "updated_at ASC",

		"password":               "",
		"name; DROP TABLE users": "",
		"(SELECT 1)":             "",
		"":                       "",
		"-":                      "",
	}
	for in, want := range cases {
		if got := rs.sortClause(in); got != want {
			t.Errorf("sortClause(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWhereClauseBuildsFiltersSearchAndSoftDelete(t *testing.T) {
	rs := &Resource{
		SoftDelete: true,
		Filters:    []string{"status", "brand_id"},
		Searchable: []string{"name", "name_en"},
	}
	req := httptest.NewRequest("GET", "/?status=published&brand_id=3&q=mac&ignored=1", nil)
	clause, args := rs.whereClause(req)

	for _, want := range []string{"deleted_at IS NULL", "status = ?", "brand_id = ?", "name LIKE ?", "name_en LIKE ?"} {
		if !strings.Contains(clause, want) {
			t.Errorf("clause %q is missing %q", clause, want)
		}
	}
	if strings.Contains(clause, "ignored") {
		t.Error("an unregistered query parameter leaked into the WHERE clause")
	}

	if len(args) != 4 {
		t.Fatalf("args = %#v, want 4", args)
	}
	if args[2] != "%mac%" || args[3] != "%mac%" {
		t.Errorf("search args = %#v, want wrapped wildcards", args[2:])
	}
}

func TestWhereClauseIsEmptyWithoutConstraints(t *testing.T) {
	rs := &Resource{}
	clause, args := rs.whereClause(httptest.NewRequest("GET", "/", nil))
	if clause != "" || len(args) != 0 {
		t.Errorf("unconstrained resource produced %q / %#v", clause, args)
	}
}

func TestWhereClauseAppliesRowLevelSecurity(t *testing.T) {
	rs := &Resource{Owner: "user_id"}

	req := httptest.NewRequest("GET", "/", nil)
	ctx := middleware.WithUser(context.Background(), 7, "ali@example.com", "customer")
	clause, args := rs.whereClause(req.WithContext(ctx))
	if !strings.Contains(clause, "user_id = ?") || len(args) != 1 || args[0] != 7 {
		t.Errorf("customer scoping = %q / %#v", clause, args)
	}

	ctx = middleware.WithUser(context.Background(), 1, "admin@yourstore.com", "admin")
	clause, _ = rs.whereClause(req.WithContext(ctx))
	if strings.Contains(clause, "user_id") {
		t.Errorf("admin should not be owner-scoped, got %q", clause)
	}

	clause, _ = rs.whereClause(req)
	if strings.Contains(clause, "user_id") {
		t.Errorf("anonymous caller should not be owner-scoped, got %q", clause)
	}
}

func TestCanTouchEnforcesOwnership(t *testing.T) {
	rs := &Resource{Owner: "user_id"}
	req := httptest.NewRequest("GET", "/", nil)

	customer := req.WithContext(middleware.WithUser(context.Background(), 7, "ali@example.com", "customer"))
	if !rs.canTouch(customer, map[string]any{"user_id": int64(7)}) {
		t.Error("owner should be able to touch their own row")
	}
	if rs.canTouch(customer, map[string]any{"user_id": int64(8)}) {
		t.Error("customer must not touch someone else's row")
	}
	if rs.canTouch(customer, map[string]any{"user_id": nil}) {
		t.Error("an unowned row must not be touchable by a customer")
	}
	if rs.canTouch(customer, map[string]any{"user_id": "7"}) {
		t.Error("a non-numeric owner column must fail closed")
	}
	if !rs.canTouch(customer, map[string]any{"user_id": 7}) {
		t.Error("plain int owner ids should be accepted")
	}

	for _, role := range []string{"admin", "super_admin", "support", "editor"} {
		staff := req.WithContext(middleware.WithUser(context.Background(), 1, "staff@yourstore.com", role))
		if !rs.canTouch(staff, map[string]any{"user_id": int64(99)}) {
			t.Errorf("%s should bypass ownership", role)
		}
	}

	if rs.canTouch(req, map[string]any{"user_id": int64(7)}) {
		t.Error("anonymous caller must not pass the ownership check")
	}

	if !(&Resource{}).canTouch(req, nil) {
		t.Error("ownerless resource should always be touchable")
	}
}

func TestBindCollectsValidationIssues(t *testing.T) {
	rs := &Resource{Fields: []Field{
		{Name: "name", Kind: KindString, Required: true},
		{Name: "slug", Kind: KindSlug, Required: true, From: "name"},
		{Name: "price", Kind: KindInt, Required: true},
		{Name: "active", Kind: KindBool, Default: int64(1)},
		{Name: "meta", Kind: KindJSON},
	}}

	cols, vals, issues := rs.bind(map[string]any{"name": "Blue Shirt", "price": float64(1000)}, true)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %+v", issues)
	}
	got := map[string]any{}
	for i, c := range cols {
		got[c] = vals[i]
	}
	if got["slug"] != "blue-shirt" {
		t.Errorf("slug = %#v, want derived blue-shirt", got["slug"])
	}
	if got["price"] != int64(1000) {
		t.Errorf("price = %#v, want int64 1000", got["price"])
	}
	if _, present := got["meta"]; present {
		t.Error("an absent optional field should not be written")
	}

	_, _, issues = rs.bind(map[string]any{}, true)
	reported := map[string]string{}
	for _, is := range issues {
		reported[is.Field] = is.Issue
	}
	for _, f := range []string{"name", "price"} {
		if reported[f] == "" {
			t.Errorf("missing %q was not reported: %+v", f, issues)
		}
	}

	_, _, issues = rs.bind(map[string]any{"name": "   ", "slug": "x", "price": float64(1)}, true)
	if len(issues) != 1 || issues[0].Field != "name" || !strings.Contains(issues[0].Issue, "empty") {
		t.Errorf("blank required string: %+v", issues)
	}

	_, _, issues = rs.bind(map[string]any{"name": "x", "slug": "x", "price": "not-a-number"}, true)
	if len(issues) != 1 || issues[0].Field != "price" {
		t.Errorf("type error: %+v", issues)
	}

	cols, _, issues = rs.bind(map[string]any{"price": float64(50)}, false)
	if len(issues) != 0 || len(cols) != 1 || cols[0] != "price" {
		t.Errorf("partial update bound %v with issues %+v", cols, issues)
	}
}

func TestBindUsesDefaultsForAbsentRequiredFields(t *testing.T) {
	rs := &Resource{Fields: []Field{
		{Name: "status", Kind: KindString, Required: true, Default: "draft"},
	}}
	cols, vals, issues := rs.bind(map[string]any{}, true)
	if len(issues) != 0 {
		t.Fatalf("a default should satisfy a required field: %+v", issues)
	}
	if len(cols) != 1 || cols[0] != "status" || vals[0] != "draft" {
		t.Errorf("default not applied: %v = %v", cols, vals)
	}
}

func TestMarkUpdatedAtRegistersTables(t *testing.T) {
	MarkUpdatedAt("sweep_fake_table")
	rs := &Resource{Table: "sweep_fake_table"}
	if !rs.touchUpdatedAt() {
		t.Error("registered table should report an updated_at column")
	}
	if !rs.hasColumn("updated_at") {
		t.Error("hasColumn should see the registered updated_at")
	}
	other := &Resource{Table: "sweep_unregistered_table"}
	if other.touchUpdatedAt() || other.hasColumn("updated_at") {
		t.Error("unregistered table must not claim updated_at")
	}
}

func TestValidEmail(t *testing.T) {
	valid := []string{
		"ali@example.com",
		"admin@yourstore.com",
		"a.b+tag@sub.example.co.uk",
		"x_y@example.io",
		"UPPER@EXAMPLE.COM",
		"digits123@example123.com",
	}
	for _, e := range valid {
		if !validEmail(e) {
			t.Errorf("validEmail(%q) = false, want true", e)
		}
	}

	invalid := []string{
		"",
		"   ",
		"not-an-email",
		"@example.com",
		"user@",
		"two@@example.com",
		"spaces in@example.com",
		"tab\tin@example.com",
		"newline\nin@example.com",
		"a@b",
		"a@b..c",
		"a@.com",
		"a@com.",
		"Name <a@b.com>",
		strings.Repeat("x", 250) + "@example.com",
	}
	for _, e := range invalid {
		if validEmail(e) {
			t.Errorf("validEmail(%q) = true, want false", e)
		}
	}
}
