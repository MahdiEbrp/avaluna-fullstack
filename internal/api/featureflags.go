package api

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerFlagsModule(rt *Router, c *core.Core) {
	rt.Module("31-feature-flags")

	rt.Handle(http.MethodGet, "/feature-flags", "", func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(), `SELECT key,enabled,rollout,roles FROM feature_flags`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		subject := flagSubject(r)
		role := middleware.Role(r.Context())
		out := map[string]bool{}
		for _, row := range rows {
			out[asStr(row["key"])] = evalFlag(row, subject, role)
		}
		writeOK(w, out)
	})

	rt.Handle(http.MethodGet, "/feature-flags/{key}", "", func(w http.ResponseWriter, r *http.Request) {
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM feature_flags WHERE key=?`, r.PathValue("key"))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Feature flag not found")
			return
		}
		on := evalFlag(row, flagSubject(r), middleware.Role(r.Context()))
		if !on {
			httpx.Fail(w, httpx.CodeFlagDisabled, "")
			return
		}
		writeOK(w, map[string]any{"key": row["key"], "enabled": true, "rollout": row["rollout"]})
	})

	flags := &Resource{
		Name: "admin/feature-flags", Table: "feature_flags",
		Fields: []Field{
			{Name: "key", Kind: KindString, Required: true},
			{Name: "description", Kind: KindString},
			{Name: "enabled", Kind: KindBool, Default: false},
			{Name: "rollout", Kind: KindInt, Default: 100},
			{Name: "roles", Kind: KindString},
		},
		Searchable: []string{"key", "description"}, OrderBy: "key",
		ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	flags.Mount(rt, c.DB)

	rt.Handle(http.MethodPost, "/admin/feature-flags/{key}/toggle", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		res, err := c.DB.Write(r.Context(),
			`UPDATE feature_flags SET enabled = 1 - enabled, updated_at=datetime('now') WHERE key=?`, key)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			httpx.Fail(w, httpx.CodeNotFound, "Feature flag not found")
			return
		}
		row, _ := c.DB.MapOne(r.Context(), `SELECT * FROM feature_flags WHERE key=?`, key)
		c.CacheClear("flag:")
		audit(c, r, "flag.toggle", "feature_flags", key, map[string]any{"enabled": row["enabled"]})
		httpx.Success(w, http.StatusOK, "Feature flag toggled", row, nil)
	})

	rt.Handle(http.MethodGet, "/ab-tests/{key}/assign", "", func(w http.ResponseWriter, r *http.Request) {
		test, err := c.DB.MapOne(r.Context(), `SELECT * FROM ab_tests WHERE key=? AND status='running'`, r.PathValue("key"))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Test not found or not running")
			return
		}
		variants := splitCSV(asStr(test["variants"]))
		if len(variants) == 0 {
			variants = []string{"A", "B"}
		}
		variant := variants[int(hashBucket(asStr(test["key"])+":"+flagSubject(r))*uint32(len(variants))/100)%len(variants)]
		_, _ = c.DB.Write(r.Context(),
			`INSERT INTO ab_results(test_id,variant,impressions) VALUES(?,?,1)
			 ON CONFLICT(test_id,variant) DO UPDATE SET impressions = impressions + 1`, test["id"], variant)
		writeOK(w, map[string]any{"test": test["key"], "variant": variant})
	})

	rt.Handle(http.MethodPost, "/ab-tests/{key}/convert", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		variant := sVal(m, "variant")
		test, err := c.DB.MapOne(r.Context(), `SELECT id FROM ab_tests WHERE key=?`, r.PathValue("key"))
		if err != nil || variant == "" {
			httpx.Fail(w, httpx.CodeValidation, "Unknown test or missing variant")
			return
		}
		_, _ = c.DB.Write(r.Context(),
			`INSERT INTO ab_results(test_id,variant,conversions) VALUES(?,?,1)
			 ON CONFLICT(test_id,variant) DO UPDATE SET conversions = conversions + 1`, test["id"], variant)
		httpx.NoBody(w, "Conversion recorded")
	})

	tests := &Resource{
		Name: "admin/ab-tests", Table: "ab_tests",
		Fields: []Field{
			{Name: "key", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "variants", Kind: KindString, Default: "A,B"},
			{Name: "status", Kind: KindString, Default: "running"},
		},
		Searchable: []string{"key", "name"}, OrderBy: "id DESC",
		ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	tests.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/admin/ab-tests/{key}/results", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		test, err := c.DB.MapOne(r.Context(), `SELECT * FROM ab_tests WHERE key=? OR id=?`,
			r.PathValue("key"), r.PathValue("key"))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT variant,impressions,conversions FROM ab_results WHERE test_id=? ORDER BY variant`, test["id"])
		best, bestRate := "", -1.0
		for _, row := range rows {
			imp, conv := asInt(row["impressions"]), asInt(row["conversions"])
			rate := 0.0
			if imp > 0 {
				rate = float64(conv) / float64(imp) * 100
			}
			row["conversionRate"] = math.Round(rate*100) / 100
			if rate > bestRate {
				best, bestRate = asStr(row["variant"]), rate
			}
		}
		writeOK(w, map[string]any{"test": test, "results": rows,
			"winner": best, "winnerRate": math.Round(bestRate*100) / 100})
	})
}

func flagSubject(r *http.Request) string {
	if uid := middleware.UserID(r.Context()); uid > 0 {
		return "u" + toString(uid)
	}
	if tok := middleware.CartToken(r.Context()); tok != "" {
		return "c" + tok
	}
	return "ip" + middleware.ClientIP(r)
}

func hashBucket(s string) uint32 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint32(sum[:4]) % 100
}

func evalFlag(row map[string]any, subject, role string) bool {
	if asInt(row["enabled"]) != 1 {
		return false
	}
	if roles := strings.TrimSpace(asStr(row["roles"])); roles != "" {
		allowed := false
		for _, want := range splitCSV(roles) {
			if strings.EqualFold(want, role) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	rollout := asInt(row["rollout"])
	if rollout >= 100 {
		return true
	}
	if rollout <= 0 {
		return false
	}
	return int(hashBucket(asStr(row["key"])+":"+subject)) < rollout
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func FeatureEnabled(ctx context.Context, c *core.Core, key, subject, role string) bool {
	row, err := c.DB.MapOne(ctx, `SELECT * FROM feature_flags WHERE key=?`, key)
	if err != nil {
		return false
	}
	return evalFlag(row, subject, role)
}
