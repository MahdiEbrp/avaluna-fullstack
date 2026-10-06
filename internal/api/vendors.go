package api

import (
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerVendorsModule(rt *Router, c *core.Core) {
	rt.Module("14-vendors")

	rt.Handle(http.MethodPost, "/vendors/register", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		shop := sVal(m, "shopName")
		if shop == "" {
			httpx.Fail(w, httpx.CodeValidation, "", httpx.FieldIssue{Field: "shopName", Issue: "is required"})
			return
		}
		if n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM vendors WHERE user_id=?`, uid); n > 0 {
			httpx.Fail(w, httpx.CodeConflict, "A vendor profile already exists for this account")
			return
		}
		slug := Slugify(shop)
		if slug == "" {
			slug = "vendor-" + toString(uid)
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO vendors(user_id,slug,shop_name,shop_name_en,description,commission_rate,status)
			 VALUES(?,?,?,?,?,?,'pending')`,
			uid, slug, shop, sVal(m, "shopNameEn"), sVal(m, "description"),
			float64(c.SettingInt("default_commission_percent", 10)))
		if err != nil {
			httpx.Fail(w, conflictOr(err), err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "vendor.register", "vendors", toString(id), map[string]any{"shop": shop})
		writeCreated(w, map[string]any{"id": id, "slug": slug, "status": "pending"})
	})

	rt.Handle(http.MethodGet, "/vendors", "", func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM vendors WHERE status='approved'`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT id,slug,shop_name,shop_name_en,description,logo_url,rating
			FROM vendors WHERE status='approved' ORDER BY rating DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/vendors/me", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "No vendor profile found")
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodPatch, "/vendors/me", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		m, _ := body(r)
		_, _ = c.DB.Write(r.Context(), `UPDATE vendors SET
			shop_name=COALESCE(NULLIF(?,''),shop_name),
			shop_name_en=COALESCE(NULLIF(?,''),shop_name_en),
			description=COALESCE(NULLIF(?,''),description),
			logo_url=COALESCE(NULLIF(?,''),logo_url),
			updated_at=datetime('now') WHERE id=?`,
			sVal(m, "shopName"), sVal(m, "shopNameEn"), sVal(m, "description"), sVal(m, "logoUrl"), row["id"])
		updated, _ := c.DB.MapOne(r.Context(), `SELECT * FROM vendors WHERE id=?`, row["id"])
		httpx.Success(w, http.StatusOK, "Vendor profile updated", updated, nil)
	})

	rt.Handle(http.MethodGet, "/vendors/me/products", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM products WHERE vendor_id=? AND deleted_at IS NULL`, row["id"])
		rows, _ := c.DB.Maps(r.Context(), `SELECT id,slug,name,sku,price,status,sold_count,rating_avg
			FROM products WHERE vendor_id=? AND deleted_at IS NULL ORDER BY id DESC LIMIT ? OFFSET ?`,
			row["id"], limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodGet, "/vendors/me/orders", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		rows, _ := c.DB.Maps(r.Context(), `
			SELECT o.id,o.number,o.status,o.payment_status,o.created_at,
			       SUM(oi.total) AS vendor_total, COUNT(oi.id) AS item_count
			FROM order_items oi JOIN orders o ON o.id=oi.order_id
			WHERE oi.vendor_id=? GROUP BY o.id ORDER BY o.id DESC LIMIT 100`, row["id"])
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/vendors/me/earnings", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		rate := asFloat(row["commission_rate"])
		stats, _ := c.DB.MapOne(r.Context(), `
			SELECT COALESCE(SUM(oi.total),0) AS gross, COUNT(DISTINCT o.id) AS orders
			FROM order_items oi JOIN orders o ON o.id=oi.order_id
			WHERE oi.vendor_id=? AND o.payment_status='paid'`, row["id"])
		gross := asInt(stats["gross"])
		commission := int(float64(gross) * rate / 100)
		payouts, _ := c.DB.Maps(r.Context(), `SELECT id,amount,status,created_at,paid_at FROM vendor_payouts
			WHERE vendor_id=? ORDER BY id DESC LIMIT 20`, row["id"])
		paid, _ := c.DB.Count(r.Context(),
			`SELECT COALESCE(SUM(amount),0) FROM vendor_payouts WHERE vendor_id=? AND status='paid'`, row["id"])
		writeOK(w, map[string]any{
			"gross": gross, "commissionRate": rate, "commission": commission,
			"net": gross - commission, "paidOut": paid, "available": gross - commission - paid,
			"orders": stats["orders"], "payouts": payouts,
		})
	})

	rt.Handle(http.MethodPost, "/vendors/me/payout-request", roleVendor, func(w http.ResponseWriter, r *http.Request) {
		row, err := myVendor(c, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		if amount <= 0 {
			httpx.Fail(w, httpx.CodeValidation, "amount must be positive")
			return
		}
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO vendor_payouts(vendor_id,amount,status,iban,note) VALUES(?,?,'requested',?,?)`,
			row["id"], amount, sVal(m, "iban"), sVal(m, "note"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "vendor.payout_request", "vendor_payouts", toString(id), map[string]any{"amount": amount})
		writeCreated(w, map[string]any{"id": id, "status": "requested", "amount": amount})
	})

	rt.Handle(http.MethodGet, "/admin/vendors", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM vendors`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT v.*, u.email FROM vendors v JOIN users u ON u.id=v.user_id
			ORDER BY v.id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	for _, action := range []string{"approve", "suspend"} {
		act := action
		rt.Handle(http.MethodPatch, "/admin/vendors/{id}/"+act, roleAdmin, func(w http.ResponseWriter, r *http.Request) {
			id, _ := httpx.PathInt(r, "id")
			status := "approved"
			if act == "suspend" {
				status = "suspended"
			}
			if _, err := c.DB.Write(r.Context(),
				`UPDATE vendors SET status=?, updated_at=datetime('now') WHERE id=?`, status, id); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			var uid int
			_ = c.DB.Row(r.Context(), `SELECT user_id FROM vendors WHERE id=?`, []any{id}, &uid)
			if status == "approved" && uid > 0 {
				_, _ = c.DB.Write(r.Context(),
					`UPDATE users SET role='vendor' WHERE id=? AND role IN ('customer','guest')`, uid)
			}
			c.Notify(r.Context(), uid, "vendor", "وضعیت فروشندگی / Vendor status",
				strings.ToUpper(status), "/vendor")
			audit(c, r, "vendor."+act, "vendors", toString(id), nil)
			httpx.NoBody(w, "Vendor "+status)
		})
	}

	rt.Handle(http.MethodPatch, "/admin/vendor-payouts/{id}", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		m, _ := body(r)
		status := sVal(m, "status")
		if status == "" {
			status = "paid"
		}
		paidAt := "NULL"
		if status == "paid" {
			paidAt = "datetime('now')"
		}
		if _, err := c.DB.Write(r.Context(),
			`UPDATE vendor_payouts SET status=?, paid_at=`+paidAt+` WHERE id=?`, status, id); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "vendor.payout_"+status, "vendor_payouts", toString(id), nil)
		httpx.NoBody(w, "Payout "+status)
	})
}

func myVendor(c *core.Core, r *http.Request) (map[string]any, error) {
	return c.DB.MapOne(r.Context(), `SELECT * FROM vendors WHERE user_id=?`, middleware.UserID(r.Context()))
}

func registerReturnsModule(rt *Router, c *core.Core) {
	rt.Module("15-returns")

	rt.Handle(http.MethodPost, "/returns", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		orderID := iVal(m, "orderId", 0)
		if orderID == 0 {
			httpx.Fail(w, httpx.CodeValidation, "orderId is required")
			return
		}
		order, err := c.DB.MapOne(r.Context(), `SELECT * FROM orders WHERE id=? AND user_id=?`, orderID, uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Order not found")
			return
		}
		if asStr(order["payment_status"]) != "paid" {
			httpx.Fail(w, httpx.CodeConflict, "Only paid orders can be returned")
			return
		}
		number := genNumber("RMA")
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO returns(number,order_id,user_id,items_json,reason,status) VALUES(?,?,?,?,?,'requested')`,
			number, orderID, uid, jsonOr(m["items"], "[]"), sVal(m, "reason"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		c.Notify(r.Context(), uid, "returns", "درخواست مرجوعی ثبت شد / Return requested", number, "/account/returns")
		DispatchWebhook(r.Context(), c, "return.requested", map[string]any{"returnId": id, "orderId": orderID})
		writeCreated(w, map[string]any{"id": id, "number": number, "status": "requested"})
	})

	rt.Handle(http.MethodGet, "/returns", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT r.*, o.number AS order_number FROM returns r
			JOIN orders o ON o.id=r.order_id WHERE r.user_id=? ORDER BY r.id DESC`, middleware.UserID(r.Context()))
		writeOK(w, rows)
	})

	rt.Handle(http.MethodGet, "/returns/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM returns WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if asInt(row["user_id"]) != middleware.UserID(r.Context()) && !isStaff(r) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		writeOK(w, row)
	})

	rt.Handle(http.MethodGet, "/admin/returns", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM returns`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT r.*, o.number AS order_number, u.email
			FROM returns r JOIN orders o ON o.id=r.order_id LEFT JOIN users u ON u.id=r.user_id
			ORDER BY r.id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	for _, action := range []string{"approve", "reject"} {
		act := action
		rt.Handle(http.MethodPatch, "/admin/returns/{id}/"+act, roleAdmin, func(w http.ResponseWriter, r *http.Request) {
			id, _ := httpx.PathInt(r, "id")
			m, _ := body(r)
			status := "approved"
			if act == "reject" {
				status = "rejected"
			}
			if _, err := c.DB.Write(r.Context(),
				`UPDATE returns SET status=?, admin_note=?, updated_at=datetime('now') WHERE id=?`,
				status, sVal(m, "note"), id); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
			var uid int
			_ = c.DB.Row(r.Context(), `SELECT COALESCE(user_id,0) FROM returns WHERE id=?`, []any{id}, &uid)
			c.Notify(r.Context(), uid, "returns", "وضعیت مرجوعی / Return "+status, sVal(m, "note"), "/account/returns")
			audit(c, r, "return."+act, "returns", toString(id), nil)
			httpx.NoBody(w, "Return "+status)
		})
	}

	rt.Handle(http.MethodPost, "/admin/returns/{id}/refund", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "id")
		ret, err := c.DB.MapOne(r.Context(), `SELECT * FROM returns WHERE id=?`, id)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		if amount <= 0 {
			var total int
			_ = c.DB.Row(r.Context(), `SELECT grand_total FROM orders WHERE id=?`, []any{ret["order_id"]}, &total)
			amount = total
		}
		pay, err := c.DB.MapOne(r.Context(),
			`SELECT id FROM payments WHERE order_id=? AND status='succeeded' ORDER BY id DESC LIMIT 1`, ret["order_id"])
		if err == nil {
			if err := RefundPayment(r.Context(), c, asInt(pay["id"]), amount); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		} else if uid := asInt(ret["user_id"]); uid > 0 {
			if _, err := WalletCredit(r.Context(), c, uid, amount, "refund", asStr(ret["number"])); err != nil {
				httpx.Fail(w, httpx.CodeInternal, err.Error())
				return
			}
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE returns SET status='refunded', refund_amount=?, updated_at=datetime('now') WHERE id=?`, amount, id)
		audit(c, r, "return.refund", "returns", toString(id), map[string]any{"amount": amount})
		httpx.Success(w, http.StatusOK, "Refund processed", map[string]any{"amount": amount}, nil)
	})
}
