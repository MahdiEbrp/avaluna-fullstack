package api

import (
	"context"
	"net/http"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerReviewsModule(rt *Router, c *core.Core) {
	rt.Module("11-reviews")

	rt.Handle(http.MethodGet, "/products/{id}/reviews", "", func(w http.ResponseWriter, r *http.Request) {
		pid, err := resolveProductID(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM reviews WHERE product_id=? AND status='approved'`, pid)
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT r.id,r.rating,r.title,r.body,r.verified,r.helpful,r.created_at,
			       COALESCE(NULLIF(TRIM(u.first_name || ' ' || u.last_name),''),'Guest') AS author
			FROM reviews r LEFT JOIN users u ON u.id = r.user_id
			WHERE r.product_id=? AND r.status='approved' ORDER BY r.helpful DESC, r.id DESC LIMIT ? OFFSET ?`,
			pid, limit, offset)
		dist, _ := c.DB.Maps(r.Context(),
			`SELECT rating, COUNT(*) AS count FROM reviews WHERE product_id=? AND status='approved' GROUP BY rating`, pid)
		httpx.Success(w, http.StatusOK, "OK", rows, &httpx.Meta{
			Pagination: ptr(httpx.MakePagination(page, limit, total)),
			Extra:      map[string]any{"distribution": dist},
		})
	})

	rt.Handle(http.MethodPost, "/products/{id}/reviews", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		pid, err := resolveProductID(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		rating := iVal(m, "rating", 0)
		if rating < 1 || rating > 5 {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "rating", Issue: "must be between 1 and 5"})
			return
		}
		if n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM reviews WHERE product_id=? AND user_id=?`, pid, uid); n > 0 {
			httpx.Fail(w, httpx.CodeConflict, "You already reviewed this product")
			return
		}

		verified := 0
		if n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM order_items oi JOIN orders o ON o.id=oi.order_id
			WHERE oi.product_id=? AND o.user_id=? AND o.payment_status='paid'`, pid, uid); n > 0 {
			verified = 1
		}
		if verified == 0 && c.SettingBool("reviews_require_purchase", false) {
			httpx.Fail(w, httpx.CodeForbidden, "Only verified purchasers can review this product")
			return
		}
		status := "pending"
		if c.SettingBool("reviews_auto_approve", true) {
			status = "approved"
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO reviews(product_id,user_id,rating,title,body,verified,status) VALUES(?,?,?,?,?,?,?)`,
			pid, uid, rating, sVal(m, "title"), sVal(m, "body"), verified, status)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		RecalcProductRating(r.Context(), c, pid)
		writeCreated(w, map[string]any{"id": id, "status": status, "verified": verified == 1})
	})

	rt.Handle(http.MethodPatch, "/reviews/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM reviews WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if asInt(row["user_id"]) != middleware.UserID(r.Context()) && !isStaff(r) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		m, _ := body(r)
		_, _ = c.DB.Write(r.Context(),
			`UPDATE reviews SET rating=COALESCE(NULLIF(?,0),rating), title=?, body=?, updated_at=datetime('now') WHERE id=?`,
			iVal(m, "rating", 0), sVal(m, "title"), sVal(m, "body"), id)
		RecalcProductRating(r.Context(), c, asInt(row["product_id"]))
		httpx.NoBody(w, "Review updated")
	})

	rt.Handle(http.MethodDelete, "/reviews/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM reviews WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if asInt(row["user_id"]) != middleware.UserID(r.Context()) && !isStaff(r) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		_, _ = c.DB.Write(r.Context(), `DELETE FROM reviews WHERE id=?`, id)
		RecalcProductRating(r.Context(), c, asInt(row["product_id"]))
		httpx.NoBody(w, "Review deleted")
	})

	rt.Handle(http.MethodPost, "/reviews/{id}/helpful", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `UPDATE reviews SET helpful = helpful + 1 WHERE id=?`, id)
		httpx.NoBody(w, "Thanks for the feedback")
	})

	rt.Handle(http.MethodPost, "/reviews/{id}/report", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		_, _ = c.DB.Write(r.Context(), `UPDATE reviews SET reports = reports + 1 WHERE id=?`, id)
		audit(c, r, "review.report", "reviews", toString(id), nil)
		httpx.NoBody(w, "Report submitted")
	})

	rt.Handle(http.MethodGet, "/admin/reviews", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		status := r.URL.Query().Get("status")
		where, args := "", []any{}
		if status != "" {
			where, args = "WHERE r.status=?", []any{status}
		}
		total, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM reviews r "+where, args...)
		rows, _ := c.DB.Maps(r.Context(), `SELECT r.*, p.name AS product_name,
			COALESCE(u.email,'guest') AS author_email FROM reviews r
			LEFT JOIN products p ON p.id=r.product_id LEFT JOIN users u ON u.id=r.user_id `+
			where+` ORDER BY r.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodPatch, "/admin/reviews/{id}/approve", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		m, _ := body(r)
		status := sVal(m, "status")
		if status == "" {
			status = "approved"
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE reviews SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
		var pid int
		_ = c.DB.Row(r.Context(), `SELECT product_id FROM reviews WHERE id=?`, []any{id}, &pid)
		RecalcProductRating(r.Context(), c, pid)
		audit(c, r, "review.moderate", "reviews", toString(id), map[string]any{"status": status})
		httpx.NoBody(w, "Review "+status)
	})
}

func RecalcProductRating(ctx context.Context, c *core.Core, productID int) {
	row, err := c.DB.MapOne(ctx,
		`SELECT COALESCE(AVG(rating),0) AS avg, COUNT(*) AS cnt FROM reviews WHERE product_id=? AND status='approved'`, productID)
	if err != nil {
		return
	}
	_, _ = c.DB.Write(ctx, `UPDATE products SET rating_avg=?, rating_count=? WHERE id=?`,
		asFloat(row["avg"]), asInt(row["cnt"]), productID)
}

func registerWishlistModule(rt *Router, c *core.Core) {
	rt.Module("12-wishlist")

	rt.Handle(http.MethodGet, "/wishlist", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT wi.id,wi.created_at,p.id AS product_id,p.slug,p.name,p.name_en,p.price,p.compare_price,
			       p.thumbnail,p.rating_avg,p.currency
			FROM wishlist_items wi JOIN products p ON p.id = wi.product_id
			WHERE wi.user_id=? AND p.deleted_at IS NULL ORDER BY wi.id DESC`, middleware.UserID(r.Context()))
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/wishlist/{productId}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		pid, ok := httpx.PathInt(r, "productId")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid product id")
			return
		}
		_, err := c.DB.Write(r.Context(), `INSERT OR IGNORE INTO wishlist_items(user_id,product_id) VALUES(?,?)`,
			middleware.UserID(r.Context()), pid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM wishlist_items WHERE user_id=?`, middleware.UserID(r.Context()))
		httpx.Success(w, http.StatusCreated, "Added to wishlist", map[string]any{"count": n}, nil)
	})

	rt.Handle(http.MethodDelete, "/wishlist/{productId}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		pid, _ := httpx.PathInt(r, "productId")
		_, _ = c.DB.Write(r.Context(), `DELETE FROM wishlist_items WHERE user_id=? AND product_id=?`,
			middleware.UserID(r.Context()), pid)
		n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM wishlist_items WHERE user_id=?`, middleware.UserID(r.Context()))
		httpx.Success(w, http.StatusOK, "Removed from wishlist", map[string]any{"count": n}, nil)
	})

	rt.Handle(http.MethodGet, "/compare", "", func(w http.ResponseWriter, r *http.Request) {
		tok := compareToken(w, r)
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT p.id,p.slug,p.name,p.name_en,p.price,p.thumbnail,p.attrs_json,p.rating_avg,
			       b.name AS brand_name, c.name AS category_name
			FROM compare_items ci JOIN products p ON p.id = ci.product_id
			LEFT JOIN brands b ON b.id=p.brand_id LEFT JOIN categories c ON c.id=p.category_id
			WHERE ci.token=? ORDER BY ci.id`, tok)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/compare/{productId}", "", func(w http.ResponseWriter, r *http.Request) {
		pid, ok := httpx.PathInt(r, "productId")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid product id")
			return
		}
		tok := compareToken(w, r)
		n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM compare_items WHERE token=?`, tok)
		if n >= 4 {
			httpx.Fail(w, httpx.CodeConflict, "Compare list holds at most 4 products")
			return
		}
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO compare_items(token,product_id) VALUES(?,?)`, tok, pid)
		httpx.Success(w, http.StatusCreated, "Added to compare", map[string]any{"count": n + 1}, nil)
	})

	rt.Handle(http.MethodDelete, "/compare/{productId}", "", func(w http.ResponseWriter, r *http.Request) {
		pid, _ := httpx.PathInt(r, "productId")
		tok := compareToken(w, r)
		_, _ = c.DB.Write(r.Context(), `DELETE FROM compare_items WHERE token=? AND product_id=?`, tok, pid)
		httpx.NoBody(w, "Removed from compare")
	})
}

const CompareCookie = "ecms_compare"

func compareToken(w http.ResponseWriter, r *http.Request) string {
	if ck, err := r.Cookie(CompareCookie); err == nil && ck.Value != "" {
		return ck.Value
	}
	tok := auth.RandomToken(12)
	if w != nil {
		http.SetCookie(w, &http.Cookie{Name: CompareCookie, Value: tok, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 7 * 24 * 3600})
	}
	return tok
}
