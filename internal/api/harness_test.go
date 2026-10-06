package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/testsupport"
)

type envelope struct {
	Success    bool            `json:"success"`
	StatusCode int             `json:"statusCode"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
	Meta       struct {
		Pagination *struct {
			Page       int `json:"page"`
			Limit      int `json:"limit"`
			Total      int `json:"total"`
			TotalPages int `json:"totalPages"`
		} `json:"pagination"`
	} `json:"meta"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details any    `json:"details"`
	} `json:"error"`
	Timestamp string `json:"timestamp"`
}

func call(t *testing.T, env *testsupport.Env, method, path, body, token string) (*httptest.ResponseRecorder, envelope) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	env.Handler.ServeHTTP(rec, req)

	var e envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("%s %s: response is not a JSON envelope: %v\n%.300s", method, path, err, rec.Body.String())
	}
	return rec, e
}

func bearer(t *testing.T, env *testsupport.Env, email, pass string) string {
	t.Helper()
	_, e := call(t, env, http.MethodPost, "/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, email, pass), "")
	if !e.Success {
		t.Fatalf("login %s failed: %+v", email, e.Error)
	}
	var data struct {
		Tokens struct {
			AccessToken string `json:"accessToken"`
		} `json:"tokens"`
	}
	_ = json.Unmarshal(e.Data, &data)
	if data.Tokens.AccessToken == "" {
		t.Fatalf("no access token in login response: %s", e.Data)
	}
	return data.Tokens.AccessToken
}

func newEnv(t *testing.T) *testsupport.Env {
	t.Helper()
	return testsupport.New(t, testsupport.NoRateLimit())
}

func adminToken(t *testing.T, env *testsupport.Env) string {
	t.Helper()
	return bearer(t, env, "admin@yourstore.com", "Admin@12345")
}

func customerToken(t *testing.T, env *testsupport.Env) string {
	t.Helper()
	return bearer(t, env, "ali@example.com", "Customer@12345")
}

func scalar(t *testing.T, env *testsupport.Env, q string, args ...any) string {
	t.Helper()
	v, err := env.DB.Scalar(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("scalar %q: %v", q, err)
	}
	if !v.Valid {
		return ""
	}
	return v.String
}

func dataOf(t *testing.T, rec *httptest.ResponseRecorder) []byte {
	t.Helper()
	var e struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return e.Data
}

func testContext() context.Context { return context.Background() }

func mustExec(t *testing.T, env *testsupport.Env, q string, args ...any) {
	t.Helper()
	if _, err := env.DB.Write(testContext(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

func scalarInt(t *testing.T, env *testsupport.Env, q string, args ...any) int {
	t.Helper()
	n, err := env.DB.Count(testContext(), q, args...)
	if err != nil {
		t.Fatalf("scalar %q: %v", q, err)
	}
	return n
}
