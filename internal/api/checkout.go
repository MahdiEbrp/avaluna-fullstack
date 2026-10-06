package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerCheckoutModule(rt *Router, c *core.Core) {
	rt.Module("06-checkout")
	lim := rt.Limit("checkout", 10, time.Minute)
	idem := middleware.Idempotency(idemStore{c}, false)

	rt.HandleWith(http.MethodPost, "/checkout/initiate", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		cart, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		if len(sum.Items) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "Cart is empty")
			return
		}
		for _, it := range sum.Items {
			if it.OutOfStock {
				httpx.Fail(w, httpx.CodeOutOfStock, "One or more items are no longer available",
					httpx.FieldIssue{Field: "variantId", Issue: fmt.Sprintf("%s: only %d left", it.Name, it.Stock)})
				return
			}
		}
		addresses, _ := c.DB.Maps(r.Context(),
			`SELECT * FROM addresses WHERE user_id=? AND deleted_at IS NULL ORDER BY is_default DESC, id DESC`,
			middleware.UserID(r.Context()))
		methods, _ := c.DB.Maps(r.Context(), `SELECT * FROM shipping_methods WHERE active=1 ORDER BY sort_order`)
		gateways, _ := c.DB.Maps(r.Context(), `SELECT code,name,name_en,provider,fee_fixed,fee_percent FROM payment_methods WHERE active=1 ORDER BY sort_order`)
		writeOK(w, map[string]any{
			"cart": sum, "addresses": addresses, "shippingMethods": methods, "paymentMethods": gateways,
			"checkoutToken": sum.Token,
		})
	}, lim)

	rt.HandleWith(http.MethodPost, "/checkout/shipping-method", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		mid := iVal(m, "shippingMethodId", 0)
		cart, _ := EnsureCart(r.Context(), c, w, r)
		sum, _ := PriceCart(r.Context(), c, cart, mid)
		writeOK(w, sum)
	}, lim)

	rt.HandleWith(http.MethodPost, "/checkout/payment-method", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		code := sVal(m, "paymentMethod")
		row, err := c.DB.MapOne(r.Context(), `SELECT code,name,provider,fee_fixed,fee_percent FROM payment_methods WHERE code=? AND active=1`, code)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, "Unknown payment method")
			return
		}
		writeOK(w, row)
	}, lim)

	rt.HandleWith(http.MethodPost, "/checkout/complete", RoleAuthenticated, handleCheckoutComplete(c), lim, idem)

	rt.Handle(http.MethodGet, "/orders", RoleAuthenticated, handleMyOrders(c))
	rt.Handle(http.MethodGet, "/orders/{id}", RoleAuthenticated, handleOrderDetail(c))
	rt.Handle(http.MethodGet, "/orders/{id}/invoice", RoleAuthenticated, handleOrderInvoice(c))
	rt.Handle(http.MethodGet, "/orders/{id}/track", RoleAuthenticated, handleOrderTrack(c))
	rt.Handle(http.MethodPost, "/orders/{id}/cancel", RoleAuthenticated, handleOrderCancel(c))
	rt.Handle(http.MethodPost, "/orders/{id}/reorder", RoleAuthenticated, handleReorder(c))
}

type idemStore struct{ c *core.Core }

func (s idemStore) Lookup(ctx context.Context, key string) (int, string, bool) {
	var status int
	var bodyJSON string
	err := s.c.DB.Row(ctx, `SELECT status_code,response_json FROM idempotency_keys WHERE key=?`, []any{key}, &status, &bodyJSON)
	if err != nil {
		return 0, "", false
	}
	return status, bodyJSON, true
}

func (s idemStore) Reserve(ctx context.Context, key string, userID int, endpoint string) (bool, error) {
	res, err := s.c.DB.Write(ctx,
		`INSERT OR IGNORE INTO idempotency_keys(key,user_id,endpoint,response_json,status_code)
		 VALUES(?,?,?,'',0)`, key, nullIf(userID), endpoint)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s idemStore) Release(ctx context.Context, key string) error {
	_, err := s.c.DB.Write(ctx,
		`DELETE FROM idempotency_keys WHERE key=? AND status_code=0`, key)
	return err
}

func (s idemStore) Save(ctx context.Context, key string, userID int, endpoint string, status int, bodyJSON string) error {
	_, err := s.c.DB.Write(ctx,
		`INSERT INTO idempotency_keys(key,user_id,endpoint,response_json,status_code)
		 VALUES(?,?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET response_json=excluded.response_json, status_code=excluded.status_code`,
		key, nullIf(userID), endpoint, bodyJSON, status)
	return err
}

func handleCheckoutComplete(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		cart, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		shipID := iVal(m, "shippingMethodId", 0)
		sum, err := PriceCart(r.Context(), c, cart, shipID)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if len(sum.Items) == 0 {
			httpx.Fail(w, httpx.CodeValidation, "Cart is empty")
			return
		}

		var addr map[string]any
		if aid := iVal(m, "addressId", 0); aid > 0 {
			row, err := c.DB.MapOne(r.Context(), `SELECT * FROM addresses WHERE id=? AND user_id=? AND deleted_at IS NULL`, aid, uid)
			if err != nil {
				httpx.Fail(w, httpx.CodeValidation, "Address not found")
				return
			}
			addr = row
		} else if inline, ok := m["address"].(map[string]any); ok {
			addr = inline
		} else {
			httpx.Fail(w, httpx.CodeValidation, "addressId or address object is required")
			return
		}

		payMethod := sVal(m, "paymentMethod")
		if payMethod == "" {
			payMethod = "wallet"
		}
		if n, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM payment_methods WHERE code=? AND active=1`, payMethod); n == 0 {
			httpx.Fail(w, httpx.CodeValidation, "Unknown or disabled payment method")
			return
		}

		for _, it := range sum.Items {
			if it.OutOfStock {
				httpx.Fail(w, httpx.CodeOutOfStock, "Stock changed during checkout",
					httpx.FieldIssue{Field: "variantId", Issue: it.Name})
				return
			}
		}

		email := middleware.UserEmail(r.Context())
		number := genNumber("ORD")
		res, err := c.DB.Write(r.Context(), `INSERT INTO orders
			(number,user_id,email,status,payment_status,currency,subtotal,discount_total,tax_total,
			 shipping_total,grand_total,coupon_code,shipping_method_id,payment_method,address_json,notes,placed_at)
			VALUES(?,?,?,'pending','unpaid',?,?,?,?,?,?,?,NULLIF(?,0),?,?,?,datetime('now'))`,
			number, uid, email, sum.Currency, sum.Subtotal, sum.Discount, sum.Tax,
			sum.Shipping, sum.GrandTotal, sum.Coupon, shipID, payMethod, jsonStr(addr), sVal(m, "notes"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		oid64, _ := res.LastInsertId()
		orderID := int(oid64)

		for _, it := range sum.Items {
			var cost int
			_ = c.DB.Row(r.Context(), `SELECT cost_price FROM products WHERE id=?`, []any{it.ProductID}, &cost)
			_, _ = c.DB.Write(r.Context(), `INSERT INTO order_items
				(order_id,product_id,variant_id,vendor_id,name,sku,quantity,unit_price,cost_price,discount,tax,total)
				VALUES(?,?,NULLIF(?,0),NULLIF(?,0),?,?,?,?,?,0,0,?)`,
				orderID, it.ProductID, it.VariantID, it.VendorID, it.Name, it.SKU,
				it.Quantity, it.UnitPrice, cost, it.LineTotal)
			if it.VariantID > 0 {

				if err := ReserveStock(r.Context(), c, it.VariantID, it.Quantity, number); err != nil {
					c.LogError(r.Context(), "warn", "STOCK_OVERSELL", err.Error(),
						r.URL.Path, "order "+number)
					_, _ = c.DB.Write(r.Context(),
						`INSERT INTO order_events(order_id,status,note) VALUES(?,'stock_shortfall',?)`,
						orderID, err.Error())
				}
			}
			_, _ = c.DB.Write(r.Context(), `UPDATE products SET sold_count = sold_count + ? WHERE id=?`, it.Quantity, it.ProductID)
		}
		_, _ = c.DB.Write(r.Context(), `INSERT INTO order_events(order_id,status,note,actor_id) VALUES(?,'pending','Order placed',?)`, orderID, uid)

		if sum.Coupon != "" {

			if err := RedeemCoupon(r.Context(), c, sum.Coupon, uid, orderID, sum.Discount); err != nil {
				c.LogError(r.Context(), "warn", "COUPON_EXHAUSTED",
					sum.Coupon+": "+err.Error(), r.URL.Path, "order "+number)
				_, _ = c.DB.Write(r.Context(),
					`INSERT INTO order_events(order_id,status,note) VALUES(?,'coupon_rejected',?)`,
					orderID, "Coupon "+sum.Coupon+" could not be redeemed: "+err.Error())
			}
		}

		_, _ = c.DB.Write(r.Context(), `UPDATE carts SET status='converted', updated_at=datetime('now') WHERE id=?`, cart)

		payment, payErr := CreatePaymentSession(r.Context(), c, orderID, uid, payMethod, sum.GrandTotal, sum.Currency)
		if payErr != nil {
			httpx.Fail(w, httpx.CodePaymentFailed, payErr.Error())
			return
		}

		AwardLoyaltyPoints(r.Context(), c, uid, sum.GrandTotal, "order "+number)
		c.Notify(r.Context(), uid, "order", "سفارش ثبت شد / Order placed",
			"شماره سفارش: "+number, "/account/orders/"+toString(orderID))
		DispatchWebhook(r.Context(), c, "order.created", map[string]any{
			"orderId": orderID, "number": number, "total": sum.GrandTotal, "currency": sum.Currency,
		})
		audit(c, r, "order.create", "orders", toString(orderID), map[string]any{"total": sum.GrandTotal})

		order, _ := c.DB.MapOne(r.Context(), `SELECT * FROM orders WHERE id=?`, orderID)
		httpx.Success(w, http.StatusCreated, "Order placed", map[string]any{
			"order": order, "payment": payment,
		}, nil)
	}
}

func CheckoutCompleteHandler(c *core.Core) http.HandlerFunc { return handleCheckoutComplete(c) }

func loadOrderForUser(c *core.Core, r *http.Request) (map[string]any, bool) {
	id, ok := httpx.PathInt(r, "id")
	if !ok {
		return nil, false
	}
	row, err := c.DB.MapOne(r.Context(), `SELECT * FROM orders WHERE id=?`, id)
	if err != nil {
		return nil, false
	}
	uid := middleware.UserID(r.Context())
	if asInt(row["user_id"]) != uid && !isStaff(r) {
		return nil, false
	}
	return row, true
}

func handleOrderDetail(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, ok := loadOrderForUser(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		items, _ := c.DB.Maps(r.Context(), `SELECT * FROM order_items WHERE order_id=?`, order["id"])
		events, _ := c.DB.Maps(r.Context(), `SELECT status,note,created_at FROM order_events WHERE order_id=? ORDER BY id`, order["id"])
		payments, _ := c.DB.Maps(r.Context(), `SELECT reference,gateway,amount,status,created_at FROM payments WHERE order_id=?`, order["id"])
		shipment, _ := c.DB.MapOne(r.Context(), `SELECT * FROM shipments WHERE order_id=? ORDER BY id DESC LIMIT 1`, order["id"])
		writeOK(w, map[string]any{"order": order, "items": items, "events": events,
			"payments": payments, "shipment": shipment})
	}
}

func handleOrderInvoice(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, ok := loadOrderForUser(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		items, _ := c.DB.Maps(r.Context(), `SELECT name,sku,quantity,unit_price,total FROM order_items WHERE order_id=?`, order["id"])
		if strings.Contains(r.Header.Get("Accept"), "text/html") || r.URL.Query().Get("format") == "html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(renderInvoiceHTML(c, order, items)))
			return
		}
		writeOK(w, map[string]any{
			"invoiceNumber": "INV-" + asStr(order["number"]),
			"issuedAt":      time.Now().UTC().Format(time.RFC3339),
			"order":         order, "items": items,
			"seller": map[string]any{
				"name": c.Setting("site_name", c.Cfg.AppName),
				"vat":  c.Setting("vat_number", "IR-0000000000"),
			},
			"downloadUrl": fmt.Sprintf("%s/orders/%v/invoice?format=html", c.Cfg.APIPrefix, order["id"]),
		})
	}
}

func renderInvoiceHTML(c *core.Core, order map[string]any, items []map[string]any) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html dir="rtl"><head><meta charset="utf-8"><title>Invoice ` +
		asStr(order["number"]) + `</title><style>
	body{font-family:system-ui,Segoe UI,Tahoma;padding:32px;color:#0f172a}
	table{width:100%;border-collapse:collapse;margin-top:16px}
	th,td{border:1px solid #cbd5e1;padding:8px;text-align:right;font-size:14px}
	th{background:#f1f5f9}h1{font-size:22px}.tot{font-weight:700}
	@media print{.noprint{display:none}}</style></head><body>`)
	b.WriteString(`<h1>فاکتور فروش / Invoice ` + asStr(order["number"]) + `</h1>`)
	b.WriteString(`<p>` + c.Setting("site_name", c.Cfg.AppName) + ` — ` + asStr(order["created_at"]) + `</p>`)
	b.WriteString(`<table><tr><th>کالا / Item</th><th>SKU</th><th>تعداد</th><th>قیمت واحد</th><th>جمع</th></tr>`)
	for _, it := range items {
		b.WriteString(fmt.Sprintf("<tr><td>%v</td><td>%v</td><td>%v</td><td>%v</td><td>%v</td></tr>",
			it["name"], it["sku"], it["quantity"], it["unit_price"], it["total"]))
	}
	b.WriteString(fmt.Sprintf(`</table>
	<p>جمع کل: %v</p><p>تخفیف: %v</p><p>مالیات: %v</p><p>ارسال: %v</p>
	<p class="tot">مبلغ نهایی: %v %v</p>
	<button class="noprint" onclick="window.print()">Print</button></body></html>`,
		order["subtotal"], order["discount_total"], order["tax_total"], order["shipping_total"],
		order["grand_total"], order["currency"]))
	return b.String()
}

func handleOrderTrack(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, ok := loadOrderForUser(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		shipment, err := c.DB.MapOne(r.Context(), `SELECT * FROM shipments WHERE order_id=? ORDER BY id DESC LIMIT 1`, order["id"])
		if err != nil {
			writeOK(w, map[string]any{"status": order["status"], "trackingCode": order["tracking_code"], "events": []any{}})
			return
		}
		writeOK(w, shipment)
	}
}

func handleOrderCancel(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, ok := loadOrderForUser(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		status := asStr(order["status"])
		if status == "shipped" || status == "delivered" || status == "cancelled" {
			httpx.Fail(w, httpx.CodeConflict, "Order can no longer be cancelled")
			return
		}
		if err := CancelOrder(r.Context(), c, asInt(order["id"]), "cancelled by "+middleware.Role(r.Context())); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "order.cancel", "orders", asStr(order["id"]), nil)
		httpx.NoBody(w, "Order cancelled")
	}
}

func CancelOrder(ctx context.Context, c *core.Core, orderID int, note string) error {
	items, err := c.DB.Maps(ctx, `SELECT COALESCE(variant_id,0) AS variant_id,quantity FROM order_items WHERE order_id=?`, orderID)
	if err != nil {
		return err
	}
	for _, it := range items {
		if vid := asInt(it["variant_id"]); vid > 0 {
			_ = ReleaseStock(ctx, c, vid, asInt(it["quantity"]), false, "order-cancel")
		}
	}
	if _, err := c.DB.Write(ctx,
		`UPDATE orders SET status='cancelled', cancelled_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, orderID); err != nil {
		return err
	}
	_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'cancelled',?)`, orderID, note)
	DispatchWebhook(ctx, c, "order.cancelled", map[string]any{"orderId": orderID})
	return nil
}

func handleReorder(c *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, ok := loadOrderForUser(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		cart, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		items, _ := c.DB.Maps(r.Context(), `SELECT product_id,COALESCE(variant_id,0) AS variant_id,quantity FROM order_items WHERE order_id=?`, order["id"])
		added, skipped := 0, 0
		for _, it := range items {
			if err := AddToCart(r.Context(), c, cart, asInt(it["product_id"]), asInt(it["variant_id"]), asInt(it["quantity"])); err != nil {
				skipped++
				continue
			}
			added++
		}
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK,
			fmt.Sprintf("%d items added, %d unavailable", added, skipped), sum, nil)
	}
}

func MarkOrderPaid(ctx context.Context, c *core.Core, orderID int) error {
	if _, err := c.DB.Write(ctx,
		`UPDATE orders SET status='paid', payment_status='paid', paid_at=datetime('now'),
		 updated_at=datetime('now') WHERE id=?`, orderID); err != nil {
		return err
	}
	_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'paid','Payment confirmed')`, orderID)
	var uid int
	_ = c.DB.Row(ctx, `SELECT COALESCE(user_id,0) FROM orders WHERE id=?`, []any{orderID}, &uid)
	c.Notify(ctx, uid, "order", "پرداخت تایید شد / Payment confirmed", "", "/account/orders/"+toString(orderID))
	DispatchWebhook(ctx, c, "order.paid", map[string]any{"orderId": orderID})
	return nil
}
