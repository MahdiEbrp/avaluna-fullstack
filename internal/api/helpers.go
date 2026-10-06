package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func writeOK(w http.ResponseWriter, data any)      { httpx.OK(w, data) }
func writeCreated(w http.ResponseWriter, data any) { httpx.Created(w, data) }

func errorCodeTable() []map[string]any { return httpx.Codes() }

func body(r *http.Request) (map[string]any, error) {
	var m map[string]any
	if err := httpx.DecodeLoose(r, &m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func sVal(m map[string]any, k string) string { return strings.TrimSpace(toString(m[k])) }

func iVal(m map[string]any, k string, def int) int {
	v, ok := m[k]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(t)); err == nil {
			return n
		}
	}
	return def
}

func bVal(m map[string]any, k string, def bool) bool {
	v, ok := m[k]
	if !ok {
		return def
	}
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		return s == "1" || s == "true" || s == "on" || s == "yes"
	}
	return def
}

func requireFields(m map[string]any, keys ...string) []httpx.FieldIssue {
	var issues []httpx.FieldIssue
	for _, k := range keys {
		if sVal(m, k) == "" {
			issues = append(issues, httpx.FieldIssue{Field: k, Issue: "is required"})
		}
	}
	return issues
}

func validEmail(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}
	domain := s[strings.LastIndex(s, "@")+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1 && !strings.Contains(domain, "..")
}

func nowUTC() string { return time.Now().UTC().Format("2006-01-02 15:04:05") }

func asInt(v any) int {
	switch t := v.(type) {
	case int64:
		return int(t)
	case int:
		return t
	case float64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	}
	return 0
}

func asStr(v any) string {
	if v == nil {
		return ""
	}
	return toString(v)
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func jsonStr(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func genNumber(prefix string) string {
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().UTC().Format("060102"),
		strings.ToUpper(randHex(2)))
}

func randHex(n int) string {
	const hexd = "0123456789ABCDEF"
	b := make([]byte, n*2)
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = hexd[(seed>>33)&15]
	}
	return string(b)
}

func audit(c *core.Core, r *http.Request, action, entity, entityID string, meta any) {
	c.Audit(r.Context(), middleware.UserID(r.Context()), middleware.Role(r.Context()),
		action, entity, entityID, middleware.ClientIP(r), meta)
}

func userRow(ctx context.Context, c *core.Core, id int) (map[string]any, error) {
	return c.DB.MapOne(ctx, `SELECT id,uuid,email,phone,first_name,last_name,role,status,
		email_verified_at,twofa_enabled,locale,currency,referral_code,last_login_at,created_at
		FROM users WHERE id = ? AND deleted_at IS NULL`, id)
}

func settingsPublic(c *core.Core) map[string]string {
	all := c.AllSettings("public")
	out := make(map[string]string, len(all))
	for k, v := range all {
		if looksSensitive(k) {
			continue
		}
		out[k] = v
	}
	return out
}

func looksSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, marker := range []string{"secret", "password", "token", "api_key", "apikey", "private_key", "pepper"} {
		if strings.Contains(k, marker) {
			return true
		}
	}
	return false
}

func paginate(r *http.Request) (page, limit, offset int) { return httpx.PageParams(r) }
