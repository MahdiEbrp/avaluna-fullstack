package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerAdminModule(rt *Router, c *core.Core) {
	rt.Module("19-admin")

	rt.Handle(http.MethodGet, "/admin/stats/overview", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, DashboardStats(r.Context(), c))
	})

	rt.Handle(http.MethodGet, "/admin/stats/sales", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		days := httpx.QueryInt(r, "days", 30, 1, 365)
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT DATE(created_at) AS date, COUNT(*) AS orders,
			       COALESCE(SUM(grand_total),0) AS revenue
			FROM orders WHERE payment_status='paid' AND created_at >= datetime('now', ?)
			GROUP BY DATE(created_at) ORDER BY date`, fmt.Sprintf("-%d days", days))
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/admin/stats/top-products", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT oi.product_id, oi.name, SUM(oi.quantity) AS units, SUM(oi.total) AS revenue
			FROM order_items oi JOIN orders o ON o.id=oi.order_id
			WHERE o.payment_status='paid' GROUP BY oi.product_id ORDER BY revenue DESC LIMIT 20`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/admin/stats/customers", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT u.id,u.email,u.first_name,u.last_name,u.created_at,
			       COUNT(o.id) AS orders, COALESCE(SUM(o.grand_total),0) AS spent
			FROM users u LEFT JOIN orders o ON o.user_id=u.id AND o.payment_status='paid'
			WHERE u.deleted_at IS NULL AND u.role='customer'
			GROUP BY u.id ORDER BY spent DESC LIMIT 50`)
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/admin/stats/abandoned-carts", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		writeOK(w, AbandonedCarts(r.Context(), c, 100))
	})

	rt.Handle(http.MethodGet, "/admin/users", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		conds, args := []string{"deleted_at IS NULL"}, []any{}
		if role := r.URL.Query().Get("role"); role != "" {
			conds, args = append(conds, "role=?"), append(args, role)
		}
		if st := r.URL.Query().Get("status"); st != "" {
			conds, args = append(conds, "status=?"), append(args, st)
		}
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			conds = append(conds, "(email LIKE ? OR first_name LIKE ? OR last_name LIKE ?)")
			args = append(args, "%"+q+"%", "%"+q+"%", "%"+q+"%")
		}
		where := "WHERE " + strings.Join(conds, " AND ")
		total, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM users "+where, args...)
		rows, _ := c.DB.Maps(r.Context(), `SELECT id,email,first_name,last_name,role,status,locale,
			twofa_enabled,last_login_at,created_at FROM users `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
			append(args, limit, offset)...)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodPatch, "/admin/users/{id}/role", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		m, _ := body(r)
		role := sVal(m, "role")
		valid := map[string]bool{auth.RoleCustomer: true, auth.RoleVendor: true, auth.RoleEditor: true,
			auth.RoleSupport: true, auth.RoleAdmin: true, auth.RoleSuperAdmin: true}
		if !valid[role] {
			httpx.Fail(w, httpx.CodeValidation, "Unknown role")
			return
		}
		if id == middleware.UserID(r.Context()) {
			httpx.Fail(w, httpx.CodeForbidden, "You cannot change your own role")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE users SET role=?, updated_at=datetime('now') WHERE id=?`, role, id)
		audit(c, r, "user.role_change", "users", toString(id), map[string]any{"role": role})
		httpx.NoBody(w, "Role updated")
	})

	rt.Handle(http.MethodPatch, "/admin/users/{id}/ban", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		m, _ := body(r)
		status := "banned"
		if bVal(m, "unban", false) {
			status = "active"
		}
		if id == middleware.UserID(r.Context()) {
			httpx.Fail(w, httpx.CodeForbidden, "You cannot ban yourself")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE users SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
		if status == "banned" {
			_, _ = c.DB.Write(r.Context(), `UPDATE refresh_tokens SET revoked_at=datetime('now') WHERE user_id=?`, id)
		}
		audit(c, r, "user."+status, "users", toString(id), nil)
		httpx.NoBody(w, "User "+status)
	})

	rt.Handle(http.MethodGet, "/admin/orders", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		conds, args := []string{"1=1"}, []any{}
		if s := r.URL.Query().Get("status"); s != "" {
			conds, args = append(conds, "o.status=?"), append(args, s)
		}
		if s := r.URL.Query().Get("paymentStatus"); s != "" {
			conds, args = append(conds, "o.payment_status=?"), append(args, s)
		}
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			conds = append(conds, "(o.number LIKE ? OR o.email LIKE ?)")
			args = append(args, "%"+q+"%", "%"+q+"%")
		}
		where := "WHERE " + strings.Join(conds, " AND ")
		total, _ := c.DB.Count(r.Context(), "SELECT COUNT(*) FROM orders o "+where, args...)
		rows, _ := c.DB.Maps(r.Context(), `SELECT o.*, COALESCE(u.email,o.email) AS customer_email,
			(SELECT COUNT(*) FROM order_items oi WHERE oi.order_id=o.id) AS item_count
			FROM orders o LEFT JOIN users u ON u.id=o.user_id `+where+
			` ORDER BY o.id DESC LIMIT ? OFFSET ?`, append(args, limit, offset)...)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodPatch, "/admin/orders/{id}/status", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		m, _ := body(r)
		status := sVal(m, "status")
		allowed := map[string]bool{"pending": true, "paid": true, "processing": true, "shipped": true,
			"delivered": true, "cancelled": true, "refunded": true}
		if !allowed[status] {
			httpx.Fail(w, httpx.CodeValidation, "Unknown order status")
			return
		}
		switch status {
		case "cancelled":
			if err := CancelOrder(r.Context(), c, id, sVal(m, "note")); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		case "paid":
			if err := MarkOrderPaid(r.Context(), c, id); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		case "shipped":
			if _, err := CreateShipment(r.Context(), c, id, sVal(m, "carrier")); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		default:
			_, _ = c.DB.Write(r.Context(), `UPDATE orders SET status=?, updated_at=datetime('now') WHERE id=?`, status, id)
			_, _ = c.DB.Write(r.Context(), `INSERT INTO order_events(order_id,status,note,actor_id) VALUES(?,?,?,?)`,
				id, status, sVal(m, "note"), middleware.UserID(r.Context()))
		}
		audit(c, r, "order.status", "orders", toString(id), map[string]any{"status": status})
		httpx.NoBody(w, "Order status updated to "+status)
	})

	rt.Handle(http.MethodGet, "/admin/logs/audit", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM audit_log`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT a.*, u.email AS actor_email FROM audit_log a
			LEFT JOIN users u ON u.id=a.actor_id ORDER BY a.id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/admin/logs/errors", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM error_log`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT * FROM error_log ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/admin/export/orders", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		exportCSV(w, r, c, "orders", `SELECT id,number,email,status,payment_status,currency,subtotal,
			discount_total,tax_total,shipping_total,grand_total,created_at FROM orders ORDER BY id DESC`)
	})
}

func DashboardStats(ctx context.Context, c *core.Core) map[string]any {
	num := func(q string, args ...any) int { n, _ := c.DB.Count(ctx, q, args...); return n }
	today := num(`SELECT COALESCE(SUM(grand_total),0) FROM orders
		WHERE payment_status='paid' AND DATE(created_at)=DATE('now')`)
	yesterday := num(`SELECT COALESCE(SUM(grand_total),0) FROM orders
		WHERE payment_status='paid' AND DATE(created_at)=DATE('now','-1 day')`)
	growth := 0.0
	if yesterday > 0 {
		growth = (float64(today) - float64(yesterday)) / float64(yesterday) * 100
	}
	series, _ := c.DB.Maps(ctx, `
		SELECT DATE(created_at) AS date, COALESCE(SUM(grand_total),0) AS revenue, COUNT(*) AS orders
		FROM orders WHERE created_at >= datetime('now','-14 days') AND payment_status='paid'
		GROUP BY DATE(created_at) ORDER BY date`)
	recent, _ := c.DB.Maps(ctx, `SELECT id,number,status,payment_status,grand_total,currency,
		COALESCE(email,'') AS email, created_at FROM orders ORDER BY id DESC LIMIT 8`)
	top, _ := c.DB.Maps(ctx, `SELECT oi.name, SUM(oi.quantity) AS units, SUM(oi.total) AS revenue
		FROM order_items oi JOIN orders o ON o.id=oi.order_id WHERE o.payment_status='paid'
		GROUP BY oi.product_id ORDER BY revenue DESC LIMIT 6`)

	return map[string]any{
		"revenueToday":   today,
		"revenueTotal":   num(`SELECT COALESCE(SUM(grand_total),0) FROM orders WHERE payment_status='paid'`),
		"revenueMonth":   num(`SELECT COALESCE(SUM(grand_total),0) FROM orders WHERE payment_status='paid' AND created_at >= datetime('now','-30 days')`),
		"growthPercent":  fmt.Sprintf("%+.1f%%", growth),
		"ordersTotal":    num(`SELECT COUNT(*) FROM orders`),
		"ordersPending":  num(`SELECT COUNT(*) FROM orders WHERE status='pending'`),
		"ordersToday":    num(`SELECT COUNT(*) FROM orders WHERE DATE(created_at)=DATE('now')`),
		"customers":      num(`SELECT COUNT(*) FROM users WHERE role='customer' AND deleted_at IS NULL`),
		"vendors":        num(`SELECT COUNT(*) FROM vendors WHERE status='approved'`),
		"products":       num(`SELECT COUNT(*) FROM products WHERE deleted_at IS NULL`),
		"lowStock":       num(`SELECT COUNT(*) FROM inventory WHERE (quantity-reserved) <= reorder_level`),
		"abandonedCarts": num(`SELECT COUNT(*) FROM carts WHERE status='active' AND updated_at < datetime('now','-1 day') AND id IN (SELECT cart_id FROM cart_items)`),
		"pendingReviews": num(`SELECT COUNT(*) FROM reviews WHERE status='pending'`),
		"openReturns":    num(`SELECT COUNT(*) FROM returns WHERE status='requested'`),
		"avgOrderValue":  num(`SELECT COALESCE(CAST(AVG(grand_total) AS INTEGER),0) FROM orders WHERE payment_status='paid'`),
		"salesSeries":    series,
		"recentOrders":   recent,
		"topProducts":    top,
		"generatedAt":    time.Now().UTC().Format(time.RFC3339),
	}
}

func AbandonedCarts(ctx context.Context, c *core.Core, limit int) []map[string]any {
	rows, _ := c.DB.Maps(ctx, `
		SELECT ca.id, ca.token, ca.updated_at, COALESCE(u.email,'guest') AS email,
		       COUNT(ci.id) AS items, COALESCE(SUM(ci.quantity*ci.unit_price),0) AS value
		FROM carts ca
		JOIN cart_items ci ON ci.cart_id = ca.id
		LEFT JOIN users u ON u.id = ca.user_id
		WHERE ca.status='active' AND ca.updated_at < datetime('now','-1 day')
		GROUP BY ca.id ORDER BY value DESC LIMIT ?`, limit)
	return rows
}

func exportCSV(w http.ResponseWriter, r *http.Request, c *core.Core, name, query string, args ...any) {
	rows, err := c.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		httpx.Fail(w, httpx.CodeInternal, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		httpx.Fail(w, httpx.CodeInternal, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s-%s.csv"`, name, time.Now().UTC().Format("20060102-150405")))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write(cols)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return
		}
		rec := make([]string, len(cols))
		for i, v := range raw {
			rec[i] = sanitizeCSV(asStr(normalizeVal(v)))
		}
		_ = cw.Write(rec)
	}
}

func normalizeVal(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func sanitizeCSV(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}
