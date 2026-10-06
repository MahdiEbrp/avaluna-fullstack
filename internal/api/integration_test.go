package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistrationLoginAndProfileLifecycle(t *testing.T) {
	env := newEnv(t)

	_, e := call(t, env, http.MethodPost, "/api/v1/auth/register",
		`{"email":"newbie@example.com","password":"Str0ng@Pass1","firstName":"Newbie"}`, "")
	if !e.Success {
		t.Fatalf("register: %+v", e.Error)
	}

	rec, e := call(t, env, http.MethodPost, "/api/v1/auth/register",
		`{"email":"newbie@example.com","password":"Str0ng@Pass1","firstName":"Dup"}`, "")
	if e.Success || rec.Code != http.StatusConflict {
		t.Errorf("duplicate registration = %d %+v, want 409", rec.Code, e.Error)
	}

	for _, pw := range []string{"", "short", "1234567"} {
		rec, e := call(t, env, http.MethodPost, "/api/v1/auth/register",
			fmt.Sprintf(`{"email":"weak-%d@example.com","password":%q,"firstName":"W"}`, len(pw), pw), "")
		if e.Success {
			t.Errorf("password %q (%d chars) was accepted (%d), want a rejection", pw, len(pw), rec.Code)
		}
	}
	if _, e := call(t, env, http.MethodPost, "/api/v1/auth/register",
		`{"email":"longsimple@example.com","password":"correcthorsebattery","firstName":"L"}`, ""); !e.Success {
		t.Errorf("a long passphrase should be accepted: %+v", e.Error)
	}

	tok := bearer(t, env, "newbie@example.com", "Str0ng@Pass1")

	rec, e = call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"newbie@example.com","password":"WrongPass@1"}`, "")
	if e.Success || rec.Code != http.StatusUnauthorized {
		t.Errorf("bad password = %d, want 401", rec.Code)
	}
	rec, _ = call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"ghost@example.com","password":"WrongPass@1"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unknown account = %d, want 401 (no account enumeration)", rec.Code)
	}

	_, e = call(t, env, http.MethodGet, "/api/v1/users/me", "", tok)
	if !e.Success {
		t.Fatalf("profile read: %+v", e.Error)
	}
	_, e = call(t, env, http.MethodPatch, "/api/v1/users/me",
		`{"firstName":"Renamed","lastName":"Person","phone":"09120001122"}`, tok)
	if !e.Success {
		t.Fatalf("profile update: %+v", e.Error)
	}
	row := scalar(t, env, `SELECT first_name||' '||last_name||' '||phone FROM users WHERE email='newbie@example.com'`)
	if row != "Renamed Person 09120001122" {
		t.Errorf("profile after PATCH = %q", row)
	}

	_, _ = call(t, env, http.MethodPatch, "/api/v1/users/me", `{"role":"super_admin","status":"banned"}`, tok)
	if got := scalar(t, env, `SELECT role FROM users WHERE email='newbie@example.com'`); got != "customer" {
		t.Errorf("role was mass-assignable: %q", got)
	}

	_, e = call(t, env, http.MethodGet, "/api/v1/auth/sessions", "", tok)
	if !e.Success {
		t.Errorf("sessions: %+v", e.Error)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	env := newEnv(t)

	var devToken string
	for _, email := range []string{"ali@example.com", "nobody@example.com"} {
		_, e := call(t, env, http.MethodPost, "/api/v1/auth/forgot-password",
			fmt.Sprintf(`{"email":%q}`, email), "")
		if !e.Success {
			t.Errorf("forgot-password for %s should report success, got %+v", email, e.Error)
		}
		var d struct {
			Sent     bool   `json:"sent"`
			DevToken string `json:"devToken"`
		}
		_ = json.Unmarshal(e.Data, &d)
		if email == "ali@example.com" {
			devToken = d.DevToken
		} else if d.DevToken != "" {
			t.Error("a reset token was issued for an unknown address")
		}
	}
	if devToken == "" {
		t.Fatal("no dev reset token was returned outside production")
	}

	if n := scalar(t, env, `SELECT COUNT(*) FROM auth_tokens WHERE purpose='reset_password'`); n != "1" {
		t.Errorf("reset challenges stored = %s, want exactly 1", n)
	}
	if n := scalar(t, env,
		`SELECT COUNT(*) FROM auth_tokens WHERE purpose='reset_password' AND token_hash=?`, devToken); n != "0" {
		t.Error("the reset token is stored in plaintext")
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/auth/reset-password",
		`{"token":"not-a-real-token","password":"Brand@New1"}`, "")
	if rec.Code < 400 {
		t.Errorf("bogus reset token accepted (%d)", rec.Code)
	}

	rec, _ = call(t, env, http.MethodPost, "/api/v1/auth/reset-password",
		fmt.Sprintf(`{"token":%q,"password":"short"}`, devToken), "")
	if rec.Code < 400 {
		t.Errorf("short replacement password accepted (%d)", rec.Code)
	}

	if _, e := call(t, env, http.MethodPost, "/api/v1/auth/reset-password",
		fmt.Sprintf(`{"token":%q,"password":"Brand@New1"}`, devToken), ""); !e.Success {
		t.Fatalf("reset with a valid token: %+v", e.Error)
	}
	if used := scalar(t, env, `SELECT used_at FROM auth_tokens WHERE purpose='reset_password'`); used == "" {
		t.Error("the consumed reset token was not marked used")
	}
	rec, _ = call(t, env, http.MethodPost, "/api/v1/auth/reset-password",
		fmt.Sprintf(`{"token":%q,"password":"Another@New1"}`, devToken), "")
	if rec.Code < 400 {
		t.Errorf("reset token was reusable (%d)", rec.Code)
	}

	if _, e := call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"ali@example.com","password":"Brand@New1"}`, ""); !e.Success {
		t.Error("the new password does not work after reset")
	}
	rec, _ = call(t, env, http.MethodPost, "/api/v1/auth/login",
		`{"email":"ali@example.com","password":"Customer@12345"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("the old password still works after reset (%d)", rec.Code)
	}
}

func TestAddressBookCRUD(t *testing.T) {
	env := newEnv(t)
	tok := customerToken(t, env)

	before := scalar(t, env, `SELECT COUNT(*) FROM addresses WHERE user_id=7`)

	_, e := call(t, env, http.MethodPost, "/api/v1/users/me/addresses",
		`{"recipient":"Test Person","phone":"09121234567","country":"IR","province":"Fars","city":"Shiraz","postal_code":"7134567890","line1":"Zand St 5"}`, tok)
	if !e.Success {
		t.Fatalf("create address: %+v", e.Error)
	}
	var created struct {
		ID int `json:"id"`
	}
	_ = json.Unmarshal(e.Data, &created)
	if created.ID == 0 {

		created.ID = atoiOrZero(scalar(t, env, `SELECT id FROM addresses WHERE user_id=7 ORDER BY id DESC LIMIT 1`))
	}

	after := scalar(t, env, `SELECT COUNT(*) FROM addresses WHERE user_id=7`)
	if before == after {
		t.Fatalf("address count unchanged (%s)", after)
	}

	path := fmt.Sprintf("/api/v1/users/me/addresses/%d", created.ID)
	_, e = call(t, env, http.MethodPatch, path, `{"city":"Esfahan"}`, tok)
	if !e.Success {
		t.Fatalf("patch address: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT city FROM addresses WHERE id=?`, created.ID); got != "Esfahan" {
		t.Errorf("city after PATCH = %q", got)
	}

	other := bearer(t, env, "zahra@example.com", "Customer@12345")
	if rec, _ := call(t, env, http.MethodGet, path, "", other); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("another customer read the address: %d", rec.Code)
	}
	if rec, _ := call(t, env, http.MethodDelete, path, "", other); rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("another customer deleted the address: %d", rec.Code)
	}

	if _, e = call(t, env, http.MethodDelete, path, "", tok); !e.Success {
		t.Fatalf("delete address: %+v", e.Error)
	}
}

func atoiOrZero(s string) int {
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func TestWalletTopupAndBalanceArithmetic(t *testing.T) {
	env := newEnv(t)
	tok := customerToken(t, env)

	start := scalar(t, env, `SELECT balance FROM wallets WHERE user_id=7`)

	_, e := call(t, env, http.MethodPost, "/api/v1/payments/wallet/topup", `{"amount":5000000}`, tok)
	if !e.Success {
		t.Fatalf("topup: %+v", e.Error)
	}
	after := scalar(t, env, `SELECT balance FROM wallets WHERE user_id=7`)
	if atoiOrZero(after)-atoiOrZero(start) != 5000000 {
		t.Errorf("balance went %s -> %s, want +5000000", start, after)
	}

	if n := scalar(t, env, `SELECT COUNT(*) FROM wallet_transactions WHERE user_id=7 AND type='topup'`); n == "0" {
		t.Error("top-up left no ledger entry")
	}

	for _, amt := range []string{"0", "-100"} {
		rec, e := call(t, env, http.MethodPost, "/api/v1/payments/wallet/topup",
			fmt.Sprintf(`{"amount":%s}`, amt), tok)
		if e.Success {
			t.Errorf("top-up of %s was accepted (%d)", amt, rec.Code)
		}
	}

	_, e = call(t, env, http.MethodGet, "/api/v1/payments/wallet/balance", "", tok)
	if !e.Success {
		t.Fatalf("balance read: %+v", e.Error)
	}
}

func TestPaymentVerificationOutcomes(t *testing.T) {
	env := newEnv(t)
	tok := customerToken(t, env)

	_, e := call(t, env, http.MethodPost, "/api/v1/cart/items", `{"productId":4,"quantity":1}`, tok)
	if !e.Success {
		t.Fatalf("add to cart: %+v", e.Error)
	}
	_, e = call(t, env, http.MethodPost, "/api/v1/checkout/complete", `{
		"shippingMethodId":1,"paymentMethod":"zarinpal",
		"address":{"recipient":"Ali","phone":"09120000000","country":"IR","province":"Tehran","city":"Tehran","postal_code":"1234567890","line1":"St 1"}}`, tok)
	if !e.Success {
		t.Fatalf("checkout: %+v", e.Error)
	}
	var out struct {
		Order   map[string]any `json:"order"`
		Payment struct {
			Reference string `json:"reference"`
		} `json:"payment"`
	}
	if err := json.Unmarshal(e.Data, &out); err != nil {
		t.Fatalf("decode checkout: %v\n%s", err, e.Data)
	}
	ref := out.Payment.Reference
	if ref == "" {
		t.Fatalf("no payment reference in %s", e.Data)
	}

	_, _ = call(t, env, http.MethodPost, "/api/v1/payments/verify",
		fmt.Sprintf(`{"reference":%q,"outcome":"failed"}`, ref), tok)
	if st := scalar(t, env, `SELECT status FROM payments WHERE reference=?`, ref); st == "succeeded" {
		t.Error("a failed verification marked the payment succeeded")
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/payments/verify",
		`{"reference":"PAY-DOESNOTEXIST0","outcome":"success"}`, tok)
	if rec.Code != http.StatusNotFound && rec.Code < 400 {
		t.Errorf("unknown reference = %d, want a 4xx", rec.Code)
	}
}

func TestPaymentMethodsAreAdvertised(t *testing.T) {
	env := newEnv(t)
	_, e := call(t, env, http.MethodGet, "/api/v1/payments/methods", "", "")
	if !e.Success {
		t.Fatalf("payment methods: %+v", e.Error)
	}
	body := string(e.Data)
	for _, want := range []string{"zarinpal", "wallet"} {
		if !strings.Contains(body, want) {
			t.Errorf("payment methods do not advertise %q: %s", want, body)
		}
	}
}

func TestInventoryAdjustmentsAreAudited(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	variant := scalar(t, env, `SELECT id FROM product_variants ORDER BY id LIMIT 1`)
	path := "/api/v1/inventory/" + variant

	_, e := call(t, env, http.MethodGet, path, "", tok)
	if !e.Success {
		t.Fatalf("read stock: %+v", e.Error)
	}
	before := atoiOrZero(scalar(t, env, `SELECT quantity FROM inventory WHERE variant_id=?`, variant))

	_, e = call(t, env, http.MethodPatch, path, `{"delta":25,"reason":"restock"}`, tok)
	if !e.Success {
		t.Fatalf("adjust stock: %+v", e.Error)
	}
	after := atoiOrZero(scalar(t, env, `SELECT quantity FROM inventory WHERE variant_id=?`, variant))
	if after-before != 25 {
		t.Errorf("quantity went %d -> %d, want +25", before, after)
	}

	if n := scalar(t, env,
		`SELECT COUNT(*) FROM inventory_history WHERE variant_id=? AND reason='manual_adjust' AND delta=25`,
		variant); n == "0" {
		t.Error("stock adjustment left no audit row naming its reason and delta")
	}

	if _, e = call(t, env, http.MethodGet, "/api/v1/inventory/history/"+variant, "", tok); !e.Success {
		t.Errorf("history: %+v", e.Error)
	}
	if _, e = call(t, env, http.MethodGet, "/api/v1/inventory/low-stock", "", tok); !e.Success {
		t.Errorf("low-stock: %+v", e.Error)
	}
}

func TestWarehouseCRUDAndTransfer(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	_, e := call(t, env, http.MethodPost, "/api/v1/inventory/warehouses",
		`{"name":"Shiraz DC","code":"SWEEP-DC","city":"Shiraz"}`, tok)
	if !e.Success {
		t.Fatalf("create warehouse: %+v", e.Error)
	}
	id := scalar(t, env, `SELECT id FROM warehouses WHERE code='SWEEP-DC'`)
	if id == "" {
		t.Fatal("warehouse was not persisted")
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/inventory/warehouses",
		`{"name":"Dup","code":"SWEEP-DC","city":"Shiraz"}`, tok)
	if rec.Code != http.StatusConflict && rec.Code < 400 {
		t.Errorf("duplicate warehouse code = %d, want a 4xx", rec.Code)
	}

	if _, e = call(t, env, http.MethodPatch, "/api/v1/inventory/warehouses/"+id, `{"city":"Esfahan"}`, tok); !e.Success {
		t.Fatalf("patch warehouse: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT city FROM warehouses WHERE id=?`, id); got != "Esfahan" {
		t.Errorf("city after PATCH = %q", got)
	}
	if _, e = call(t, env, http.MethodDelete, "/api/v1/inventory/warehouses/"+id, "", tok); !e.Success {
		t.Errorf("delete warehouse: %+v", e.Error)
	}
}

func multipartUpload(t *testing.T, field, filename string, content []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	_ = mw.Close()
	return mw.FormDataContentType(), buf.Bytes()
}

var tinyPNG = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func TestMediaUploadAcceptsImagesAndRejectsExecutables(t *testing.T) {
	env := newEnv(t)
	tok := customerToken(t, env)

	ct, body := multipartUpload(t, "file", "pixel.png", tinyPNG)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	env.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("png upload = %d: %.300s", rec.Code, rec.Body.String())
	}
	if n := scalar(t, env, `SELECT COUNT(*) FROM media WHERE filename LIKE '%pixel%' OR url LIKE '%png'`); n == "0" {
		t.Error("uploaded image was not recorded in the media table")
	}

	ct, body = multipartUpload(t, "file", "payload.sh", []byte("#!/bin/sh\nrm -rf /\n"))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	env.Handler.ServeHTTP(rec, req)
	if rec.Code < 400 {
		t.Errorf("shell script upload = %d, want a rejection", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/media/upload", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+tok)
	rec = httptest.NewRecorder()
	env.Handler.ServeHTTP(rec, req)
	if rec.Code < 400 || rec.Code >= 500 {
		t.Errorf("empty upload = %d, want a 4xx", rec.Code)
	}
}

func TestCSVImportValidatesAndReportsPerRow(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	post := func(path, filename, csv string) *httptest.ResponseRecorder {
		t.Helper()
		ct, body := multipartUpload(t, "file", filename, []byte(csv))
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", ct)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		env.Handler.ServeHTTP(rec, req)
		return rec
	}

	rec := httptest.NewRequest(http.MethodGet, "/api/v1/admin/import/products/template", nil)
	rec.Header.Set("Authorization", "Bearer "+tok)
	tpl := httptest.NewRecorder()
	env.Handler.ServeHTTP(tpl, rec)
	if tpl.Code != http.StatusOK {
		t.Fatalf("template = %d", tpl.Code)
	}
	header := strings.SplitN(strings.TrimPrefix(tpl.Body.String(), "\xEF\xBB\xBF"), "\n", 2)[0]
	if !strings.Contains(strings.ToLower(header), "name") {
		t.Fatalf("product template header looks wrong: %q", header)
	}

	if r := post("/api/v1/admin/import/nonsense", "x.csv", "a\n1\n"); r.Code != http.StatusNotFound {
		t.Errorf("unknown import type = %d, want 404", r.Code)
	}

	if r := post("/api/v1/admin/import/products", "empty.csv", header+"\n"); r.Code < 400 {
		t.Errorf("header-only CSV = %d, want a rejection", r.Code)
	}

	if r := post("/api/v1/admin/import/products", "bad.csv", "unrelated\nvalue\n"); r.Code < 400 {
		t.Errorf("CSV without the required columns = %d, want a rejection", r.Code)
	} else if !strings.Contains(strings.ToLower(r.Body.String()), "column") {
		t.Errorf("rejection should name the missing column: %.200s", r.Body.String())
	}

	cols := strings.Split(strings.TrimSpace(header), ",")
	row := func(vals map[string]string) string {
		out := make([]string, len(cols))
		for i, c := range cols {
			out[i] = vals[strings.TrimSpace(c)]
		}
		return strings.Join(out, ",")
	}

	before := scalar(t, env, `SELECT COUNT(*) FROM products`)
	good := header + "\n" + row(map[string]string{
		"sku": "SWEEP-1", "name": "Sweep Imported", "name_en": "Sweep Imported",
		"slug": "sweep-imported", "price": "1500000", "status": "draft",
	}) + "\n"
	if r := post("/api/v1/admin/import/products", "good.csv", good); r.Code >= 400 {
		t.Fatalf("well-formed import rejected (%d): %.300s", r.Code, r.Body.String())
	}
	if after := scalar(t, env, `SELECT COUNT(*) FROM products`); after == before {
		t.Errorf("product count unchanged after import (%s)", after)
	}
	if got := scalar(t, env, `SELECT name FROM products WHERE slug='sweep-imported'`); got != "Sweep Imported" {
		t.Errorf("imported product name = %q", got)
	}
	if n := scalar(t, env, `SELECT COUNT(*) FROM import_jobs WHERE type='products'`); n == "0" {
		t.Error("import left no job record")
	}
	if st := scalar(t, env, `SELECT status FROM import_jobs ORDER BY id DESC LIMIT 1`); st == "running" {
		t.Error("import job was left in the running state")
	}

	beforeDry := scalar(t, env, `SELECT COUNT(*) FROM products`)
	dry := post("/api/v1/admin/import/products?dryRun=true", "dry.csv", header+"\n"+row(map[string]string{
		"sku": "SWEEP-2", "name": "Dry Run Product", "slug": "dry-run-product", "price": "999",
	})+"\n")
	if dry.Code >= 400 {
		t.Errorf("dry run rejected (%d): %.200s", dry.Code, dry.Body.String())
	}
	if afterDry := scalar(t, env, `SELECT COUNT(*) FROM products`); afterDry != beforeDry {
		t.Errorf("dryRun changed the product count %s -> %s", beforeDry, afterDry)
	}

	mixed := header + "\n" +
		row(map[string]string{"sku": "SWEEP-3", "name": "Fresh One", "slug": "sweep-fresh-one", "price": "1000"}) + "\n" +
		row(map[string]string{"sku": "SWEEP-4", "name": "Clashing", "slug": "sweep-imported", "price": "1000"}) + "\n"
	if r := post("/api/v1/admin/import/products", "mixed.csv", mixed); r.Code >= 500 {
		t.Errorf("partially invalid import = %d, want a reported result", r.Code)
	}
	if got := scalar(t, env, `SELECT COUNT(*) FROM products WHERE slug='sweep-fresh-one'`); got != "1" {
		t.Error("the valid row of a partially invalid file was not imported")
	}

	if _, e := call(t, env, http.MethodGet, "/api/v1/admin/import-jobs", "", tok); !e.Success {
		t.Error("import job listing failed")
	}
	if _, e := call(t, env, http.MethodGet, "/api/v1/admin/import/types", "", tok); !e.Success {
		t.Error("import type listing failed")
	}
}

func TestBulkProductOperations(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	rec, _ := call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"publish","ids":[]}`, tok)
	if rec.Code != http.StatusBadRequest && rec.Code < 400 {
		t.Errorf("empty ids = %d, want a 4xx", rec.Code)
	}

	_, e := call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"unpublish","ids":[1,2]}`, tok)
	if !e.Success {
		t.Fatalf("bulk unpublish: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT status FROM products WHERE id=1`); got != "draft" {
		t.Errorf("product 1 status = %q, want draft", got)
	}
	if _, e = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"publish","ids":[1,2]}`, tok); !e.Success {
		t.Fatalf("bulk publish: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT status FROM products WHERE id=1`); got != "published" {
		t.Errorf("product 1 status = %q, want published", got)
	}

	if _, e = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"delete","ids":[2]}`, tok); !e.Success {
		t.Fatalf("bulk delete: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT deleted_at FROM products WHERE id=2`); got == "" {
		t.Error("bulk delete did not set deleted_at")
	}
	if _, e = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"restore","ids":[2]}`, tok); !e.Success {
		t.Fatalf("bulk restore: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT deleted_at FROM products WHERE id=2`); got != "" {
		t.Errorf("bulk restore left deleted_at = %q", got)
	}

	rec, _ = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"price-adjust","ids":[1]}`, tok)
	if rec.Code < 400 {
		t.Errorf("price-adjust without percent = %d, want a 4xx", rec.Code)
	}
	before := atoiOrZero(scalar(t, env, `SELECT price FROM products WHERE id=1`))
	if _, e = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"price-adjust","ids":[1],"percent":10}`, tok); !e.Success {
		t.Fatalf("price-adjust: %+v", e.Error)
	}
	after := atoiOrZero(scalar(t, env, `SELECT price FROM products WHERE id=1`))
	if after <= before {
		t.Errorf("price %d -> %d, want a 10%% increase", before, after)
	}

	rec, _ = call(t, env, http.MethodPost, "/api/v1/admin/bulk/products",
		`{"action":"self-destruct","ids":[1]}`, tok)
	if rec.Code < 400 {
		t.Errorf("unknown bulk action = %d, want a 4xx", rec.Code)
	}
}

func TestBulkOrderStatusRejectsUnknownStates(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	for _, bad := range []string{`{"ids":[1],"status":"teleported"}`, `{"ids":[],"status":"shipped"}`} {
		rec, _ := call(t, env, http.MethodPost, "/api/v1/admin/bulk/orders", bad, tok)
		if rec.Code < 400 {
			t.Errorf("%s = %d, want a 4xx", bad, rec.Code)
		}
	}
	if _, e := call(t, env, http.MethodPost, "/api/v1/admin/bulk/orders",
		`{"ids":[1],"status":"processing"}`, tok); !e.Success {
		t.Errorf("valid bulk status change failed: %+v", e.Error)
	}
	if got := scalar(t, env, `SELECT status FROM orders WHERE id=1`); got != "processing" {
		t.Errorf("order 1 status = %q, want processing", got)
	}
}

func TestCSVExportsCarryBOMAndHeaders(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	for _, typ := range []string{"products", "orders", "customers"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/export/"+typ, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		env.Handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("export %s = %d", typ, rec.Code)
			continue
		}
		body := rec.Body.String()

		if !strings.HasPrefix(body, "\xEF\xBB\xBF") {
			t.Errorf("export %s is missing the UTF-8 BOM", typ)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "csv") {
			t.Errorf("export %s content-type = %q", typ, ct)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
			t.Errorf("export %s is not sent as an attachment (%q)", typ, cd)
		}
		if len(strings.Split(strings.TrimSpace(body), "\n")) < 2 {
			t.Errorf("export %s has no data rows", typ)
		}
	}
}

func TestLoyaltyAndGiftCardFlows(t *testing.T) {
	env := newEnv(t)
	tok := customerToken(t, env)

	_, e := call(t, env, http.MethodGet, "/api/v1/loyalty/me", "", tok)
	if !e.Success {
		t.Fatalf("loyalty account: %+v", e.Error)
	}
	if _, e = call(t, env, http.MethodGet, "/api/v1/loyalty/rules", "", ""); !e.Success {
		t.Errorf("loyalty rules should be public: %+v", e.Error)
	}

	rec, _ := call(t, env, http.MethodPost, "/api/v1/loyalty/redeem", `{"points":99999999}`, tok)
	if rec.Code < 400 {
		t.Errorf("over-redemption = %d, want a 4xx", rec.Code)
	}

	_, e = call(t, env, http.MethodPost, "/api/v1/gift-cards/purchase", `{"amount":1000000}`, tok)
	if !e.Success {
		t.Fatalf("gift card purchase: %+v", e.Error)
	}
	code := scalar(t, env, `SELECT code FROM gift_cards ORDER BY id DESC LIMIT 1`)
	if !strings.HasPrefix(code, "GC-") {
		t.Fatalf("gift card code = %q, want a GC- prefix", code)
	}

	if _, e = call(t, env, http.MethodPost, "/api/v1/gift-cards/redeem",
		fmt.Sprintf(`{"code":%q}`, code), tok); !e.Success {
		t.Fatalf("first redemption: %+v", e.Error)
	}
	rec, _ = call(t, env, http.MethodPost, "/api/v1/gift-cards/redeem",
		fmt.Sprintf(`{"code":%q}`, code), tok)
	if rec.Code != http.StatusConflict && rec.Code < 400 {
		t.Errorf("second redemption = %d, want 409", rec.Code)
	}

	rec, _ = call(t, env, http.MethodPost, "/api/v1/gift-cards/redeem", `{"code":"GC-000000"}`, tok)
	if rec.Code >= 500 || rec.Code < 400 {
		t.Errorf("unknown gift card = %d, want a 4xx", rec.Code)
	}

	if _, e = call(t, env, http.MethodGet, "/api/v1/referral/me", "", tok); !e.Success {
		t.Errorf("referral/me: %+v", e.Error)
	}
}

func TestFeatureFlagsAndABAssignment(t *testing.T) {
	env := newEnv(t)
	tok := adminToken(t, env)

	_, e := call(t, env, http.MethodPost, "/api/v1/admin/feature-flags",
		`{"key":"test-flag","description":"Test","enabled":false}`, tok)
	if !e.Success {
		t.Fatalf("create flag: %+v", e.Error)
	}

	rec, e := call(t, env, http.MethodGet, "/api/v1/feature-flags/test-flag", "", "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("disabled flag = %d, want 403", rec.Code)
	}
	if e.Error == nil || e.Error.Code != "FEATURE_FLAG_DISABLED" {
		t.Errorf("disabled flag error = %+v, want FEATURE_FLAG_DISABLED", e.Error)
	}

	id := scalar(t, env, `SELECT id FROM feature_flags WHERE key='test-flag'`)
	if _, e = call(t, env, http.MethodPatch, "/api/v1/admin/feature-flags/"+id, `{"enabled":true}`, tok); !e.Success {
		t.Fatalf("toggle flag: %+v", e.Error)
	}
	rec, e = call(t, env, http.MethodGet, "/api/v1/feature-flags/test-flag", "", "")
	if rec.Code != http.StatusOK || !e.Success {
		t.Errorf("flag did not flip to enabled: %d %s", rec.Code, e.Data)
	}

	rec, _ = call(t, env, http.MethodGet, "/api/v1/feature-flags/never-defined", "", "")
	if rec.Code >= 500 {
		t.Errorf("unknown flag = %d", rec.Code)
	}

	first, _ := call(t, env, http.MethodGet, "/api/v1/ab-tests/checkout-button/assign", "", tok)
	second, _ := call(t, env, http.MethodGet, "/api/v1/ab-tests/checkout-button/assign", "", tok)
	if first.Code < 400 && second.Code < 400 && first.Body.String() != second.Body.String() {
		t.Errorf("A/B assignment is not sticky:\n  %s\n  %s", first.Body.String(), second.Body.String())
	}
}

func TestPublicSettingsEndpointHidesPrivateValues(t *testing.T) {
	env := newEnv(t)

	rec, env1 := call(t, env, http.MethodGet, "/api/v1/cms/settings", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /cms/settings = %d", rec.Code)
	}
	var data map[string]any
	if err := json.Unmarshal(env1.Data, &data); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	rows, err := env.DB.Maps(context.Background(), `SELECT key FROM settings WHERE scope <> 'public'`)
	if err != nil {
		t.Fatalf("load private keys: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the seed has no private settings, so this test proves nothing")
	}
	for _, r := range rows {
		key, _ := r["key"].(string)
		if _, leaked := data[key]; leaked {
			t.Errorf("private setting %q is exposed on the public endpoint", key)
		}
	}

	for _, key := range []string{"webhook_secret", "twofa_required_for_admin", "webhook_allow_unsigned"} {
		if _, leaked := data[key]; leaked {
			t.Errorf("%q must never be public", key)
		}
	}

	for _, key := range []string{"site_name", "default_currency"} {
		if _, ok := data[key]; !ok {
			t.Errorf("public setting %q went missing", key)
		}
	}

	if err := env.Core.SetSetting(context.Background(), "stripe_api_key", "sk_live_123", "public"); err != nil {
		t.Fatalf("seed a mis-scoped credential: %v", err)
	}
	_, env2 := call(t, env, http.MethodGet, "/api/v1/cms/settings", "", "")
	var data2 map[string]any
	if err := json.Unmarshal(env2.Data, &data2); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if _, leaked := data2["stripe_api_key"]; leaked {
		t.Error("a key named like a credential was published because it was mis-scoped")
	}
}
