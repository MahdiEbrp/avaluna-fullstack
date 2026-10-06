package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func TestSuccessEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	OK(rec, map[string]any{"id": 1})
	m := decode(t, rec)

	if m["success"] != true {
		t.Error("success must be true")
	}
	if m["statusCode"].(float64) != 200 {
		t.Errorf("statusCode = %v", m["statusCode"])
	}
	if m["timestamp"] == "" || m["timestamp"] == nil {
		t.Error("timestamp is required on every envelope")
	}
	if _, ok := m["data"]; !ok {
		t.Error("data key missing")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}
}

func TestCreatedUses201(t *testing.T) {
	rec := httptest.NewRecorder()
	Created(rec, map[string]any{"id": 9})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d", rec.Code)
	}
	if decode(t, rec)["statusCode"].(float64) != 201 {
		t.Error("statusCode must mirror the HTTP status")
	}
}

func TestErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, CodeOutOfStock, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("OUT_OF_STOCK should map to 422, got %d", rec.Code)
	}
	m := decode(t, rec)
	if m["success"] != false {
		t.Error("success must be false")
	}
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatal("error object missing")
	}
	if errObj["code"] != CodeOutOfStock {
		t.Errorf("code = %v", errObj["code"])
	}
	if errObj["message"] == "" {
		t.Error("a default message should be supplied when none is given")
	}
}

func TestFieldIssuesAreIncluded(t *testing.T) {
	rec := httptest.NewRecorder()
	Fail(rec, CodeValidation, "", FieldIssue{Field: "email", Issue: "required"})
	m := decode(t, rec)
	errObj := m["error"].(map[string]any)
	details, ok := errObj["details"].([]any)
	if !ok || len(details) != 1 {
		t.Fatalf("details = %v", errObj["details"])
	}
}

func TestStatusForCoversTheWholeRegistry(t *testing.T) {
	want := map[string]int{
		CodeValidation: 400, CodeUnauthorized: 401, CodeForbidden: 403, CodeNotFound: 404,
		CodeConflict: 409, CodeOutOfStock: 422, CodeCouponInvalid: 422, CodePaymentFailed: 402,
		CodeShippingUnavail: 422, CodeRateLimit: 429, CodeIdempotency: 409,
		CodeCronNotFound: 404, CodeCronRunning: 409, CodeBackupInProgress: 409,
		CodeImportFailed: 422, CodeSubPaymentFailed: 402, CodeGiftRedeemed: 409,
		CodeFlagDisabled: 403, CodeMaintenance: 503, CodeInternal: 500,
	}
	for code, status := range want {
		if got := StatusFor(code); got != status {
			t.Errorf("StatusFor(%s) = %d, want %d", code, got, status)
		}
		if DefaultMessage(code) == "" {
			t.Errorf("code %s has no default message", code)
		}
	}
	if n := len(Codes()); n != len(want) {
		t.Errorf("the registry exposes %d codes, want %d", n, len(want))
	}
}

func TestPaginationMath(t *testing.T) {
	p := MakePagination(2, 20, 95)
	if p.Page != 2 || p.Limit != 20 || p.TotalItems != 95 || p.TotalPages != 5 {
		t.Fatalf("pagination = %+v", p)
	}
	exact := MakePagination(1, 20, 40)
	if exact.TotalPages != 2 {
		t.Errorf("40 items at 20/page = %d pages, want 2", exact.TotalPages)
	}
	empty := MakePagination(1, 20, 0)
	if empty.TotalPages != 0 {
		t.Errorf("empty result totalPages = %d, want 0", empty.TotalPages)
	}
}

func TestQueryIntClamping(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/x?limit=500&bad=abc", nil)
	if got := QueryInt(r, "limit", 20, 1, 100); got != 100 {
		t.Errorf("limit clamped to %d, want 100", got)
	}
	if got := QueryInt(r, "bad", 7, 1, 100); got != 7 {
		t.Errorf("non-numeric query = %d, want the default 7", got)
	}
	if got := QueryInt(r, "missing", 3, 1, 100); got != 3 {
		t.Errorf("missing query = %d, want 3", got)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{not json"))
	var dst map[string]any
	if err := Decode(r, &dst); err == nil {
		t.Error("Decode should reject malformed JSON")
	}
}
