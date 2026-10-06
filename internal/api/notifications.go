package api

import (
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerNotifyModule(rt *Router, c *core.Core) {
	rt.Module("17-notifications")

	rt.Handle(http.MethodGet, "/notifications", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM notifications WHERE user_id=?`, uid)
		unread, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM notifications WHERE user_id=? AND read_at IS NULL`, uid)
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT * FROM notifications WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`, uid, limit, offset)
		httpx.Success(w, http.StatusOK, "OK", rows, &httpx.Meta{
			Pagination: ptr(httpx.MakePagination(page, limit, total)),
			Extra:      map[string]any{"unread": unread},
		})
	})

	rt.Handle(http.MethodPatch, "/notifications/{id}/read", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(),
			`UPDATE notifications SET read_at=datetime('now') WHERE id=? AND user_id=?`, id, middleware.UserID(r.Context()))
		httpx.NoBody(w, "Marked as read")
	})

	rt.Handle(http.MethodPatch, "/notifications/read-all", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		_, _ = c.DB.Write(r.Context(),
			`UPDATE notifications SET read_at=datetime('now') WHERE user_id=? AND read_at IS NULL`,
			middleware.UserID(r.Context()))
		httpx.NoBody(w, "All notifications marked as read")
	})

	rt.Handle(http.MethodPost, "/notifications/subscribe", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		endpoint := sVal(m, "endpoint")
		if endpoint == "" {
			httpx.Fail(w, httpx.CodeValidation, "endpoint is required")
			return
		}
		_, err := c.DB.Write(r.Context(), `INSERT INTO push_subscriptions(user_id,endpoint,keys_json) VALUES(?,?,?)`,
			middleware.UserID(r.Context()), endpoint, jsonOr(m["keys"], "{}"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeCreated(w, map[string]any{"subscribed": true})
	})

	rt.Handle(http.MethodGet, "/notifications/preferences", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO notification_preferences(user_id) VALUES(?)`, uid)
		row, _ := c.DB.MapOne(r.Context(), `SELECT * FROM notification_preferences WHERE user_id=?`, uid)
		writeOK(w, row)
	})

	rt.Handle(http.MethodPatch, "/notifications/preferences", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO notification_preferences(user_id) VALUES(?)`, uid)
		_, err := c.DB.Write(r.Context(), `UPDATE notification_preferences SET
			email=?, sms=?, push=?, marketing=?, updated_at=datetime('now') WHERE user_id=?`,
			boolInt(bVal(m, "email", true)), boolInt(bVal(m, "sms", false)),
			boolInt(bVal(m, "push", true)), boolInt(bVal(m, "marketing", true)), uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		row, _ := c.DB.MapOne(r.Context(), `SELECT * FROM notification_preferences WHERE user_id=?`, uid)
		httpx.Success(w, http.StatusOK, "Preferences updated", row, nil)
	})

	rt.Handle(http.MethodPost, "/admin/notifications/broadcast", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		title := sVal(m, "title")
		if title == "" {
			httpx.Fail(w, httpx.CodeValidation, "title is required")
			return
		}
		role := sVal(m, "role")
		q := `SELECT id FROM users WHERE deleted_at IS NULL AND status='active'`
		args := []any{}
		if role != "" {
			q += ` AND role=?`
			args = append(args, role)
		}
		ids, err := scanIDs(r.Context(), c.DB, q, args...)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		for _, id := range ids {
			c.Notify(r.Context(), id, "broadcast", title, sVal(m, "body"), sVal(m, "link"))
		}
		audit(c, r, "notifications.broadcast", "notifications", "", map[string]any{"recipients": len(ids)})
		httpx.Success(w, http.StatusOK, "Broadcast sent", map[string]any{"recipients": len(ids)}, nil)
	})
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func registerSearchModule(rt *Router, c *core.Core) {
	rt.Module("18-search")

	rt.Handle(http.MethodGet, "/search", "", func(w http.ResponseWriter, r *http.Request) {
		term := strings.TrimSpace(r.URL.Query().Get("q"))
		if term == "" {
			httpx.Fail(w, httpx.CodeValidation, "q is required")
			return
		}
		like := "%" + term + "%"
		products, _ := c.DB.Maps(r.Context(), `
			SELECT id,slug,name,name_en,price,compare_price,thumbnail,rating_avg,'product' AS kind
			FROM products WHERE status='published' AND deleted_at IS NULL
			  AND (name LIKE ? OR name_en LIKE ? OR sku LIKE ? OR short_desc LIKE ?)
			ORDER BY sold_count DESC LIMIT 24`, like, like, like, like)
		posts, _ := c.DB.Maps(r.Context(), `
			SELECT id,slug,title AS name,title_en AS name_en,cover_url AS thumbnail,'post' AS kind
			FROM blog_posts WHERE status='published' AND deleted_at IS NULL
			  AND (title LIKE ? OR title_en LIKE ? OR tags LIKE ?) LIMIT 8`, like, like, like)
		cats, _ := c.DB.Maps(r.Context(), `
			SELECT id,slug,name,name_en,'category' AS kind FROM categories
			WHERE active=1 AND deleted_at IS NULL AND (name LIKE ? OR name_en LIKE ?) LIMIT 8`, like, like)

		_, _ = c.DB.Write(r.Context(), `INSERT INTO search_log(query,results,user_id) VALUES(?,?,?)`,
			term, len(products)+len(posts)+len(cats), nullIf(middleware.UserID(r.Context())))

		writeOK(w, map[string]any{
			"query": term, "products": products, "posts": posts, "categories": cats,
			"total": len(products) + len(posts) + len(cats),
		})
	})

	rt.Handle(http.MethodGet, "/search/suggestions", "", func(w http.ResponseWriter, r *http.Request) {
		term := strings.TrimSpace(r.URL.Query().Get("q"))
		if len(term) < 2 {
			writeOK(w, []any{})
			return
		}
		like := term + "%"
		mid := "%" + term + "%"
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT name AS text, slug, 'product' AS kind FROM products
			WHERE status='published' AND deleted_at IS NULL AND (name LIKE ? OR name_en LIKE ? OR name LIKE ?)
			ORDER BY sold_count DESC LIMIT 8`, like, like, mid)
		popular, _ := c.DB.Maps(r.Context(),
			`SELECT query AS text, COUNT(*) AS hits FROM search_log WHERE query LIKE ?
			 GROUP BY query ORDER BY hits DESC LIMIT 4`, mid)
		writeOK(w, map[string]any{"suggestions": rows, "popular": popular})
	})

	rt.Handle(http.MethodGet, "/search/filters", "", func(w http.ResponseWriter, r *http.Request) {
		cat := r.URL.Query().Get("category")
		args := []any{}
		where := "WHERE p.status='published' AND p.deleted_at IS NULL"
		if cat != "" {
			where += " AND p.category_id IN (SELECT id FROM categories WHERE slug=? OR id=?)"
			args = append(args, cat, cat)
		}
		brands, _ := c.DB.Maps(r.Context(), `SELECT b.id,b.slug,b.name,b.name_en,COUNT(p.id) AS count
			FROM products p JOIN brands b ON b.id=p.brand_id `+where+` GROUP BY b.id ORDER BY count DESC`, args...)
		cats, _ := c.DB.Maps(r.Context(), `SELECT c.id,c.slug,c.name,c.name_en,COUNT(p.id) AS count
			FROM products p JOIN categories c ON c.id=p.category_id `+where+` GROUP BY c.id ORDER BY count DESC`, args...)
		price, _ := c.DB.MapOne(r.Context(), `SELECT MIN(p.price) AS min, MAX(p.price) AS max,
			AVG(p.price) AS avg FROM products p `+where, args...)
		attrs, _ := c.DB.Maps(r.Context(), `SELECT code,name,name_en,type,values_json FROM attributes WHERE filterable=1`)
		writeOK(w, map[string]any{"brands": brands, "categories": cats, "price": price, "attributes": attrs})
	})

	rt.Handle(http.MethodPost, "/search/track", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		_, _ = c.DB.Write(r.Context(), `INSERT INTO search_log(query,results,user_id,session) VALUES(?,?,?,?)`,
			sVal(m, "query"), iVal(m, "results", 0), nullIf(middleware.UserID(r.Context())), sVal(m, "session"))
		httpx.NoBody(w, "Tracked")
	})
}
