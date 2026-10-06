package app

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/arena/avaluna/internal/api"
	"github.com/arena/avaluna/internal/i18n"
	"github.com/arena/avaluna/internal/middleware"
)

func (a *App) summary(w http.ResponseWriter, r *http.Request, shippingMethodID int) (*api.CartSummary, error) {
	cartID, err := api.EnsureCart(r.Context(), a.c, w, r)
	if err != nil {
		return nil, err
	}
	return api.PriceCart(r.Context(), a.c, cartID, shippingMethodID)
}

func (a *App) totals(sum *api.CartSummary, loc string) map[string]string {
	cur := sum.Currency
	if cur == "" {
		cur = a.c.Cfg.DefaultCurrency
	}
	m := func(n int) string { return i18n.Money(loc, cur, int64(n)) }
	return map[string]string{
		"subtotal": m(sum.Subtotal), "discount": m(sum.Discount), "tax": m(sum.Tax),
		"shipping": m(sum.Shipping), "grand": m(sum.GrandTotal),
	}
}

func (a *App) lines(sum *api.CartSummary, loc string, vd VD) []map[string]any {
	cur := sum.Currency
	if cur == "" {
		cur = a.c.Cfg.DefaultCurrency
	}
	out := make([]map[string]any, 0, len(sum.Items))
	for _, it := range sum.Items {
		name := it.Name
		if loc == "en" && it.NameEn != "" {
			name = it.NameEn
		}
		thumb := it.Thumbnail
		if thumb == "" {
			thumb = "/static/img/placeholder.svg"
		}
		out = append(out, map[string]any{
			"id": it.ItemID, "slug": it.Slug, "name": name, "sku": it.SKU,
			"quantity": it.Quantity, "thumbnail": thumb,
			"unitPrice":  i18n.Money(loc, cur, int64(it.UnitPrice)),
			"lineTotal":  i18n.Money(loc, cur, int64(it.LineTotal)),
			"outOfStock": it.OutOfStock,
			"T_save":     vd.T("common.save"), "T_remove": vd.T("cart.remove"),
		})
	}
	return out
}

func (a *App) handleCart(w http.ResponseWriter, r *http.Request) {
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	sum, err := a.summary(w, r, 0)
	if err != nil {
		a.notFound(w, r)
		return
	}
	vd["Title"] = vd.T("cart.title")
	vd["Items"] = a.lines(sum, loc, vd)
	vd["Totals"] = a.totals(sum, loc)
	vd["CouponCode"] = sum.Coupon
	vd["CouponMessage"] = sum.CouponMessage
	vd["CouponApplied"] = sum.CouponApplied
	if sum.CouponApplied {
		if sum.CouponKind == "free_shipping" {
			vd["CouponMessage"] = vd.T("cart.coupon_free_shipping")
		} else {
			vd["CouponMessage"] = vd.T("cart.coupon_applied")
		}
	}
	a.render(w, r, "cart", vd)
}

func formVal(r *http.Request, key string) string {
	if err := r.ParseForm(); err == nil {
		if v := r.FormValue(key); v != "" {
			return v
		}
	}
	return ""
}

func formInt(r *http.Request, key string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(formVal(r, key)))
	if err != nil {
		return def
	}
	return n
}

func (a *App) handleCartAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cartID, err := api.EnsureCart(ctx, a.c, w, r)
	if err != nil {
		a.fragment(w, false, err.Error())
		return
	}
	productID := formInt(r, "productId", 0)
	variantID := formInt(r, "variant", formInt(r, "variantId", 0))
	qty := formInt(r, "qty", formInt(r, "quantity", 1))
	if qty < 1 {
		qty = 1
	}
	if productID == 0 {
		a.fragment(w, false, "productId is required")
		return
	}
	row, err := a.c.DB.MapOne(ctx,
		`SELECT id,price FROM products WHERE id=? AND status='published' AND deleted_at IS NULL`, productID)
	if err != nil {
		a.fragment(w, false, "Product not found")
		return
	}
	price := num(row["price"])
	if variantID == 0 {
		_ = a.c.DB.Row(ctx, `SELECT id FROM product_variants WHERE product_id=? AND active=1 LIMIT 1`,
			[]any{productID}, &variantID)
	}
	if variantID > 0 {
		var vp int
		if err := a.c.DB.Row(ctx, `SELECT price FROM product_variants WHERE id=?`, []any{variantID}, &vp); err == nil && vp > 0 {
			price = vp
		}
	}
	var existing int
	err = a.c.DB.Row(ctx, `SELECT id FROM cart_items WHERE cart_id=? AND product_id=? AND COALESCE(variant_id,0)=?`,
		[]any{cartID, productID, variantID}, &existing)
	if err == nil {
		_, _ = a.c.DB.Write(ctx,
			`UPDATE cart_items SET quantity = quantity + ?, updated_at=datetime('now') WHERE id=?`, qty, existing)
	} else {
		_, _ = a.c.DB.Write(ctx,
			`INSERT INTO cart_items(cart_id,product_id,variant_id,quantity,unit_price) VALUES(?,?,?,?,?)`,
			cartID, productID, nilZero(variantID), qty, price)
	}
	_, _ = a.c.DB.Write(ctx, `UPDATE carts SET updated_at=datetime('now') WHERE id=?`, cartID)

	n, _ := a.c.DB.Count(ctx, `SELECT COALESCE(SUM(quantity),0) FROM cart_items WHERE cart_id=?`, cartID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "cart-updated")
	_, _ = w.Write([]byte(strconv.Itoa(n)))
}

func (a *App) handleCartUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	itemID := formInt(r, "itemId", 0)
	qty := formInt(r, "quantity", 1)
	if itemID > 0 {
		if qty <= 0 {
			_, _ = a.c.DB.Write(ctx, `DELETE FROM cart_items WHERE id=?`, itemID)
		} else {
			_, _ = a.c.DB.Write(ctx,
				`UPDATE cart_items SET quantity=?, updated_at=datetime('now') WHERE id=?`, qty, itemID)
		}
	}
	a.handleCart(w, r)
}

func (a *App) handleCartRemove(w http.ResponseWriter, r *http.Request) {
	if id := formInt(r, "itemId", 0); id > 0 {
		_, _ = a.c.DB.Write(r.Context(), `DELETE FROM cart_items WHERE id=?`, id)
	}
	a.handleCart(w, r)
}

func (a *App) handleCartCoupon(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cartID, err := api.EnsureCart(ctx, a.c, w, r)
	if err == nil {
		code := strings.ToUpper(strings.TrimSpace(formVal(r, "code")))
		_, _ = a.c.DB.Write(ctx, `UPDATE carts SET coupon_code=?, updated_at=datetime('now') WHERE id=?`, code, cartID)
	}
	a.handleCart(w, r)
}

func (a *App) handleCheckoutPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if middleware.UserID(ctx) == 0 {
		http.Redirect(w, r, "/login?next=/checkout", http.StatusSeeOther)
		return
	}
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	methodID := qInt(r, "shippingMethodId", 0)
	sum, err := a.summary(w, r, methodID)
	if err != nil || len(sum.Items) == 0 {
		http.Redirect(w, r, "/cart", http.StatusSeeOther)
		return
	}
	a.fillCheckout(w, r, vd, sum, loc, "")
	a.render(w, r, "checkout", vd)
}

func (a *App) fillCheckout(w http.ResponseWriter, r *http.Request, vd VD,
	sum *api.CartSummary, loc, errMsg string) {
	ctx := r.Context()
	ship, _ := a.c.DB.Maps(ctx,
		`SELECT id,code,name,name_en,base_cost,eta_days FROM shipping_methods WHERE active=1 ORDER BY sort_order`)
	shipOut := make([]map[string]any, 0, len(ship))
	for _, m := range ship {
		shipOut = append(shipOut, map[string]any{
			"id": m["id"], "name": localized(m, "name", loc),
			"cost": i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(num(m["base_cost"]))),
			"eta":  i18n.Num(loc, int64(num(m["eta_days"]))) + " days",
		})
	}
	pay, _ := a.c.DB.Maps(ctx,
		`SELECT code,name,name_en,provider FROM payment_methods WHERE active=1 ORDER BY sort_order`)
	payOut := make([]map[string]any, 0, len(pay))
	for _, m := range pay {
		payOut = append(payOut, map[string]any{
			"code": m["code"], "name": localized(m, "name", loc), "provider": m["provider"],
		})
	}
	addr, _ := a.c.DB.MapOne(ctx,
		`SELECT * FROM addresses WHERE user_id=? AND deleted_at IS NULL ORDER BY is_default DESC, id DESC LIMIT 1`,
		middleware.UserID(ctx))
	user, _ := vd["User"].(map[string]any)
	email := ""
	if user != nil {
		email = str(user["Email"])
	}
	vd["Title"] = vd.T("checkout.title")
	vd["Items"] = a.lines(sum, loc, vd)
	vd["Totals"] = a.totals(sum, loc)
	vd["ShippingMethods"] = shipOut
	vd["PaymentMethods"] = payOut
	vd["Error"] = errMsg
	vd["SandboxNote"] = "Sandbox gateways (ZarinPal / Stripe) do not charge real money — " +
		"after placing the order you can settle or fail the payment from the confirmation page. " +
		"Wallet payments settle instantly."
	vd["Prefill"] = map[string]any{
		"recipient":  str(addr["recipient"]),
		"phone":      str(addr["phone"]),
		"email":      email,
		"province":   str(addr["province"]),
		"city":       str(addr["city"]),
		"postalCode": str(addr["postal_code"]),
		"line1":      str(addr["line1"]),
	}
}

func (a *App) handleCheckoutSubmit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if middleware.UserID(ctx) == 0 {
		http.Redirect(w, r, "/login?next=/checkout", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	payload := map[string]any{
		"shippingMethodId": formInt(r, "shippingMethodId", 0),
		"paymentMethod":    r.FormValue("gateway"),
		"gateway":          r.FormValue("gateway"),
		"email":            r.FormValue("email"),
		"address": map[string]any{
			"recipient":   r.FormValue("recipient"),
			"phone":       r.FormValue("phone"),
			"country":     "IR",
			"province":    r.FormValue("province"),
			"city":        r.FormValue("city"),
			"postal_code": r.FormValue("postalCode"),
			"line1":       r.FormValue("line1"),
		},
	}
	status, env := a.callAPI(w, r, api.CheckoutCompleteHandler(a.c), payload)
	if status >= 400 {
		vd := a.base(w, r, "")
		loc := str(vd["Loc"])
		sum, err := a.summary(w, r, formInt(r, "shippingMethodId", 0))
		if err != nil || sum == nil {
			http.Redirect(w, r, "/cart", http.StatusSeeOther)
			return
		}
		a.fillCheckout(w, r, vd, sum, loc, envError(env))
		w.WriteHeader(http.StatusOK)
		a.render(w, r, "checkout", vd)
		return
	}
	data := envData(env)
	order, _ := data["order"].(map[string]any)
	http.Redirect(w, r, "/order/"+str(order["number"]), http.StatusSeeOther)
}

func (a *App) handleOrder(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := middleware.UserID(ctx)
	if uid == 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	order, err := a.c.DB.MapOne(ctx, `SELECT * FROM orders WHERE number=?`, r.PathValue("number"))
	if err != nil || (num(order["user_id"]) != uid && !a.isStaff(r)) {
		a.notFound(w, r)
		return
	}
	vd := a.base(w, r, "")
	loc := str(vd["Loc"])
	items, _ := a.c.DB.Maps(ctx, `SELECT name,quantity,total FROM order_items WHERE order_id=?`, order["id"])
	for _, it := range items {
		it["total"] = i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(num(it["total"])))
	}
	payment, _ := a.c.DB.MapOne(ctx,
		`SELECT reference,status FROM payments WHERE order_id=? ORDER BY id DESC LIMIT 1`, order["id"])
	ref := ""
	if str(payment["status"]) == "pending" {
		ref = str(payment["reference"])
	}
	vd["Title"] = vd.T("checkout.success")
	vd["Order"] = order
	vd["Items"] = items
	vd["GrandTotal"] = i18n.Money(loc, a.c.Cfg.DefaultCurrency, int64(num(order["grand_total"])))
	vd["PaymentRef"] = ref
	vd["SandboxNote"] = "This is a simulated gateway. Choose an outcome to settle the payment."
	a.render(w, r, "order", vd)
}

func (a *App) handlePaySimulate(w http.ResponseWriter, r *http.Request) {
	ref := formVal(r, "reference")
	outcome := formVal(r, "outcome")
	if ref == "" {
		a.fragment(w, false, "reference is required")
		return
	}
	res, err := api.VerifyPayment(r.Context(), a.c, ref, outcome)
	if err != nil {
		a.fragment(w, false, err.Error())
		return
	}
	a.fragment(w, str(res["status"]) == "succeeded",
		"Payment "+str(res["status"])+" — "+ref)
}

func (a *App) isStaff(r *http.Request) bool {
	return middleware.Role(r.Context()) == "admin" ||
		middleware.Role(r.Context()) == "super_admin" ||
		middleware.Role(r.Context()) == "support"
}
