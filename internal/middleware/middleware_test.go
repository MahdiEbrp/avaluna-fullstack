package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestChainRunsInOrder(t *testing.T) {
	var order []string
	mk := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := Chain{mk("a"), mk("b"), mk("c")}.Then(okHandler())
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if strings.Join(order, ",") != "a,b,c" {
		t.Errorf("middleware order = %v", order)
	}
}

func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	h := RequestIDMW()(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("a request id should be generated")
	}

	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", "caller-supplied")
	h.ServeHTTP(rec2, req)
	if got := rec2.Header().Get("X-Request-Id"); got != "caller-supplied" {
		t.Errorf("caller request id = %q, want it preserved", got)
	}
}

func TestRecoverTurnsPanicsInto500(t *testing.T) {
	var captured string
	h := Recover(quietLogger(), func(path, msg, stack string) { captured = msg })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("kaboom") }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(captured, "kaboom") {
		t.Errorf("the panic was not reported: %q", captured)
	}
	if !strings.Contains(rec.Body.String(), "INTERNAL_SERVER_ERROR") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SecurityHeaders(true)(okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, h := range []string{
		"Content-Security-Policy", "X-Content-Type-Options",
		"Referrer-Policy", "Permissions-Policy",
	} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options must be nosniff")
	}
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("production responses must carry HSTS")
	}
}

func TestCORSPreflight(t *testing.T) {
	h := CORS([]string{"https://shop.example.com"})(okHandler())

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/products", nil)
	req.Header.Set("Origin", "https://shop.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "https://shop.example.com" {
		t.Errorf("allowed origin = %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Code != http.StatusNoContent && rec.Code != http.StatusOK {
		t.Errorf("preflight status = %d", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/products", nil)
	req2.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Access-Control-Allow-Origin") == "https://evil.example.com" {
		t.Error("an unlisted origin must not be echoed back")
	}
}

func TestRateLimiterBlocksAfterTheQuota(t *testing.T) {
	rl := NewRateLimiter(true)
	h := rl.Limit("test", 3, time.Minute)(okHandler())

	var last int
	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		h.ServeHTTP(rec, req)
		last = rec.Code
		if i < 3 && rec.Code != http.StatusOK {
			t.Fatalf("request %d was rejected early with %d", i+1, rec.Code)
		}
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("status after the quota = %d, want 429", last)
	}
}

func TestRateLimiterIsPerClient(t *testing.T) {
	rl := NewRateLimiter(true)
	h := rl.Limit("scope", 1, time.Minute)(okHandler())

	call := func(ip string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":5000"
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if call("10.0.0.1") != http.StatusOK || call("10.0.0.2") != http.StatusOK {
		t.Error("different clients must have independent buckets")
	}
	if call("10.0.0.1") != http.StatusTooManyRequests {
		t.Error("the same client should be limited")
	}
}

func TestMaintenanceModeGate(t *testing.T) {
	on := true
	h := Maintenance(func() bool { return on })(okHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/shop", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}

	for _, p := range []string{"/api/v1/health", "/login", "/static/css/app.css", "/robots.txt"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code == http.StatusServiceUnavailable {
			t.Errorf("%s must remain reachable during maintenance", p)
		}
	}

	on = false
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/shop", nil))
	if rec2.Code != http.StatusOK {
		t.Errorf("status with maintenance off = %d", rec2.Code)
	}
}

func TestClientIPPrefersForwardedHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.1.2.3:9999"
	if got := ClientIP(req); got != "10.1.2.3" {
		t.Errorf("ClientIP = %q", got)
	}
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if got := ClientIP(req); got != "203.0.113.7" {
		t.Errorf("ClientIP with XFF = %q", got)
	}
}
