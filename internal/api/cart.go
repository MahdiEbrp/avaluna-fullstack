package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

const CartCookie = "ecms_cart"

type CartLine struct {
	ItemID     int    `json:"itemId"`
	ProductID  int    `json:"productId"`
	VariantID  int    `json:"variantId,omitempty"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	NameEn     string `json:"nameEn"`
	Thumbnail  string `json:"thumbnail"`
	SKU        string `json:"sku"`
	Quantity   int    `json:"quantity"`
	UnitPrice  int    `json:"unitPrice"`
	LineTotal  int    `json:"lineTotal"`
	Stock      int    `json:"stock"`
	VendorID   int    `json:"vendorId,omitempty"`
	TaxClass   string `json:"taxClass"`
	WeightG    int    `json:"weightG"`
	OutOfStock bool   `json:"outOfStock"`
}

type CartSummary struct {
	CartID        int        `json:"cartId"`
	Token         string     `json:"token"`
	Currency      string     `json:"currency"`
	Items         []CartLine `json:"items"`
	ItemCount     int        `json:"itemCount"`
	Subtotal      int        `json:"subtotal"`
	Discount      int        `json:"discount"`
	Tax           int        `json:"tax"`
	Shipping      int        `json:"shipping"`
	GrandTotal    int        `json:"grandTotal"`
	Coupon        string     `json:"coupon,omitempty"`
	CouponKind    string     `json:"couponKind,omitempty"`
	CouponApplied bool       `json:"couponApplied"`
	CouponMessage string     `json:"couponMessage,omitempty"`
	FreeShipping  bool       `json:"freeShipping"`
	TotalWeightG  int        `json:"totalWeightG"`
}

func registerCartModule(rt *Router, c *core.Core) {
	rt.Module("05-cart")
	lim := rt.Limit("cart", 60, time.Minute)

	rt.Handle(http.MethodGet, "/cart", "", func(w http.ResponseWriter, r *http.Request) {
		cart, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		sum, err := PriceCart(r.Context(), c, cart, 0)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, sum)
	})

	rt.Handle(http.MethodGet, "/cart/summary", "", func(w http.ResponseWriter, r *http.Request) {
		cart, _ := EnsureCart(r.Context(), c, w, r)
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		writeOK(w, map[string]any{
			"itemCount": sum.ItemCount, "subtotal": sum.Subtotal, "discount": sum.Discount,
			"tax": sum.Tax, "shipping": sum.Shipping, "grandTotal": sum.GrandTotal, "currency": sum.Currency,
		})
	})

	rt.HandleWith(http.MethodPost, "/cart/items", "", func(w http.ResponseWriter, r *http.Request) {
		m, err := body(r)
		if err != nil {
			httpx.Fail(w, httpx.CodeValidation, err.Error())
			return
		}
		productID := iVal(m, "productId", iVal(m, "product_id", 0))
		variantID := iVal(m, "variantId", iVal(m, "variant_id", 0))
		qty := iVal(m, "quantity", 1)
		if qty < 1 {
			qty = 1
		}
		if productID == 0 && variantID == 0 {
			httpx.Fail(w, httpx.CodeValidation, "productId or variantId is required")
			return
		}
		cart, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		if err := AddToCart(r.Context(), c, cart, productID, variantID, qty); err != nil {
			httpx.Fail(w, errCode(err), err.Error())
			return
		}
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusCreated, "Item added to cart", sum, nil)
	}, lim)

	rt.HandleWith(http.MethodPatch, "/cart/items/{itemId}", "", func(w http.ResponseWriter, r *http.Request) {
		id, ok := httpx.PathInt(r, "itemId")
		if !ok {
			httpx.Fail(w, httpx.CodeValidation, "Invalid item id")
			return
		}
		m, _ := body(r)
		qty := iVal(m, "quantity", 1)
		cart, _ := EnsureCart(r.Context(), c, w, r)
		if qty <= 0 {
			_, _ = c.DB.Write(r.Context(), `DELETE FROM cart_items WHERE id=? AND cart_id=?`, id, cart)
		} else {
			var variantID, productID int
			if err := c.DB.Row(r.Context(), `SELECT COALESCE(variant_id,0),product_id FROM cart_items WHERE id=? AND cart_id=?`,
				[]any{id, cart}, &variantID, &productID); err != nil {
				httpx.Fail(w, httpx.CodeNotFound, "")
				return
			}
			if stock := stockFor(r.Context(), c, productID, variantID); stock >= 0 && qty > stock {
				httpx.Fail(w, httpx.CodeOutOfStock, "",
					httpx.FieldIssue{Field: "quantity", Issue: "only " + toString(stock) + " items left in stock"})
				return
			}
			_, _ = c.DB.Write(r.Context(),
				`UPDATE cart_items SET quantity=?, updated_at=datetime('now') WHERE id=? AND cart_id=?`, qty, id, cart)
		}
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK, "Cart updated", sum, nil)
	}, lim)

	rt.HandleWith(http.MethodDelete, "/cart/items/{itemId}", "", func(w http.ResponseWriter, r *http.Request) {
		id, _ := httpx.PathInt(r, "itemId")
		cart, _ := EnsureCart(r.Context(), c, w, r)
		_, _ = c.DB.Write(r.Context(), `DELETE FROM cart_items WHERE id=? AND cart_id=?`, id, cart)
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK, "Item removed", sum, nil)
	}, lim)

	rt.Handle(http.MethodDelete, "/cart", "", func(w http.ResponseWriter, r *http.Request) {
		cart, _ := EnsureCart(r.Context(), c, w, r)
		_, _ = c.DB.Write(r.Context(), `DELETE FROM cart_items WHERE cart_id=?`, cart)
		_, _ = c.DB.Write(r.Context(), `UPDATE carts SET coupon_code='' WHERE id=?`, cart)
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK, "Cart emptied", sum, nil)
	})

	rt.Handle(http.MethodPost, "/cart/merge", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		guest, err := EnsureCart(r.Context(), c, w, r)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		merged, err := MergeGuestCart(r.Context(), c, guest, uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		sum, _ := PriceCart(r.Context(), c, merged, 0)
		httpx.Success(w, http.StatusOK, "Carts merged", sum, nil)
	})

	rt.Handle(http.MethodPost, "/cart/apply-coupon", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		code := strings.ToUpper(strings.TrimSpace(sVal(m, "code")))
		if code == "" {
			httpx.Fail(w, httpx.CodeValidation, "Coupon code is required")
			return
		}
		cart, _ := EnsureCart(r.Context(), c, w, r)
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		if _, _, err := ValidateCoupon(r.Context(), c, code, sum.Subtotal, middleware.UserID(r.Context())); err != nil {
			httpx.Fail(w, httpx.CodeCouponInvalid, err.Error())
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE carts SET coupon_code=?, updated_at=datetime('now') WHERE id=?`, code, cart)
		sum2, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK, "Coupon applied", sum2, nil)
	})

	rt.Handle(http.MethodDelete, "/cart/remove-coupon", "", func(w http.ResponseWriter, r *http.Request) {
		cart, _ := EnsureCart(r.Context(), c, w, r)
		_, _ = c.DB.Write(r.Context(), `UPDATE carts SET coupon_code='' WHERE id=?`, cart)
		sum, _ := PriceCart(r.Context(), c, cart, 0)
		httpx.Success(w, http.StatusOK, "Coupon removed", sum, nil)
	})
}

func EnsureCart(ctx context.Context, c *core.Core, w http.ResponseWriter, r *http.Request) (int, error) {
	uid := middleware.UserID(ctx)
	if uid > 0 {
		var id int
		err := c.DB.Row(ctx, `SELECT id FROM carts WHERE user_id=? AND status='active' ORDER BY id DESC LIMIT 1`, []any{uid}, &id)
		if err == nil {
			return id, nil
		}
		tok := "u" + toString(uid) + "-" + auth.RandomToken(8)
		res, err := c.DB.Write(ctx, `INSERT INTO carts(token,user_id,currency,status) VALUES(?,?,?,'active')`,
			tok, uid, c.Cfg.DefaultCurrency)
		if err != nil {
			return 0, err
		}
		id64, _ := res.LastInsertId()
		return int(id64), nil
	}

	token := ""
	if ck, err := r.Cookie(CartCookie); err == nil {
		token = ck.Value
	}
	if token != "" {
		var id int
		if err := c.DB.Row(ctx, `SELECT id FROM carts WHERE token=? AND status='active'`, []any{token}, &id); err == nil {
			return id, nil
		}
	}
	token = auth.RandomToken(16)
	res, err := c.DB.Write(ctx, `INSERT INTO carts(token,currency,status) VALUES(?,?,'active')`, token, c.Cfg.DefaultCurrency)
	if err != nil {
		return 0, err
	}
	if w != nil {
		http.SetCookie(w, &http.Cookie{
			Name: CartCookie, Value: token, Path: "/", HttpOnly: true,
			Secure: c.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
		})
	}
	id64, _ := res.LastInsertId()
	return int(id64), nil
}

type stockError struct{ msg string }

func (e stockError) Error() string { return e.msg }

func errCode(err error) string {
	if _, ok := err.(stockError); ok {
		return httpx.CodeOutOfStock
	}
	return httpx.CodeInternal
}

func stockFor(ctx context.Context, c *core.Core, productID, variantID int) int {
	if variantID > 0 {
		return AvailableStock(ctx, c, variantID)
	}
	if productID > 0 {
		n, _ := c.DB.Count(ctx, `SELECT COUNT(*) FROM product_variants WHERE product_id=?`, productID)
		if n == 0 {
			return -1
		}
		return ProductStock(ctx, c, productID)
	}
	return -1
}

func AddToCart(ctx context.Context, c *core.Core, cartID, productID, variantID, qty int) error {
	if variantID > 0 && productID == 0 {
		_ = c.DB.Row(ctx, `SELECT product_id FROM product_variants WHERE id=?`, []any{variantID}, &productID)
	}
	var price int
	var status string
	if err := c.DB.Row(ctx, `SELECT price,status FROM products WHERE id=? AND deleted_at IS NULL`,
		[]any{productID}, &price, &status); err != nil {
		return stockError{"Product not found"}
	}
	if status != "published" {
		return stockError{"Product is not available"}
	}
	if variantID > 0 {
		var vp int
		if err := c.DB.Row(ctx, `SELECT price FROM product_variants WHERE id=? AND active=1`, []any{variantID}, &vp); err == nil && vp > 0 {
			price = vp
		}
	}
	existingQty := 0
	var itemID int
	_ = c.DB.Row(ctx, `SELECT id,quantity FROM cart_items WHERE cart_id=? AND product_id=? AND COALESCE(variant_id,0)=?`,
		[]any{cartID, productID, variantID}, &itemID, &existingQty)

	if stock := stockFor(ctx, c, productID, variantID); stock >= 0 && existingQty+qty > stock {
		return stockError{"Only " + toString(stock) + " items left in stock"}
	}
	if itemID > 0 {
		_, err := c.DB.Write(ctx, `UPDATE cart_items SET quantity=quantity+?, unit_price=?, updated_at=datetime('now') WHERE id=?`,
			qty, price, itemID)
		return err
	}
	_, err := c.DB.Write(ctx, `INSERT INTO cart_items(cart_id,product_id,variant_id,quantity,unit_price)
		VALUES(?,?,NULLIF(?,0),?,?)`, cartID, productID, variantID, qty, price)
	if err == nil {
		_, _ = c.DB.Write(ctx, `UPDATE carts SET updated_at=datetime('now') WHERE id=?`, cartID)
	}
	return err
}

func MergeGuestCart(ctx context.Context, c *core.Core, guestCart, userID int) (int, error) {
	var ownerID int
	_ = c.DB.Row(ctx, `SELECT COALESCE(user_id,0) FROM carts WHERE id=?`, []any{guestCart}, &ownerID)
	if ownerID == userID {
		return guestCart, nil
	}
	var userCart int
	err := c.DB.Row(ctx, `SELECT id FROM carts WHERE user_id=? AND status='active' ORDER BY id DESC LIMIT 1`, []any{userID}, &userCart)
	if err != nil {
		_, err := c.DB.Write(ctx, `UPDATE carts SET user_id=?, updated_at=datetime('now') WHERE id=?`, userID, guestCart)
		return guestCart, err
	}
	items, err := c.DB.Maps(ctx, `SELECT product_id,COALESCE(variant_id,0) AS variant_id,quantity FROM cart_items WHERE cart_id=?`, guestCart)
	if err != nil {
		return userCart, err
	}
	for _, it := range items {
		_ = AddToCart(ctx, c, userCart, asInt(it["product_id"]), asInt(it["variant_id"]), asInt(it["quantity"]))
	}
	_, _ = c.DB.Write(ctx, `DELETE FROM carts WHERE id=?`, guestCart)
	return userCart, nil
}

func PriceCart(ctx context.Context, c *core.Core, cartID, shippingMethodID int) (*CartSummary, error) {
	head, err := c.DB.MapOne(ctx, `SELECT id,token,currency,coupon_code,COALESCE(user_id,0) AS user_id FROM carts WHERE id=?`, cartID)
	if err != nil {
		return &CartSummary{Items: []CartLine{}, Currency: c.Cfg.DefaultCurrency}, nil
	}
	rows, err := c.DB.Maps(ctx, `
		SELECT ci.id,ci.product_id,COALESCE(ci.variant_id,0) AS variant_id,ci.quantity,ci.unit_price,
		       p.slug,p.name,p.name_en,p.thumbnail,p.sku,p.tax_class,p.weight_g,COALESCE(p.vendor_id,0) AS vendor_id,
		       COALESCE(v.sku,p.sku) AS variant_sku, COALESCE(v.title,'') AS variant_title,
		       COALESCE(v.weight_g, p.weight_g) AS line_weight
		FROM cart_items ci
		JOIN products p ON p.id = ci.product_id
		LEFT JOIN product_variants v ON v.id = ci.variant_id
		WHERE ci.cart_id = ? ORDER BY ci.id`, cartID)
	if err != nil {
		return nil, err
	}

	sum := &CartSummary{
		CartID: cartID, Token: asStr(head["token"]), Currency: asStr(head["currency"]),
		Items: make([]CartLine, 0, len(rows)), Coupon: asStr(head["coupon_code"]),
	}
	for _, row := range rows {
		qty := asInt(row["quantity"])
		price := asInt(row["unit_price"])
		variantID := asInt(row["variant_id"])
		productID := asInt(row["product_id"])
		stock := stockFor(ctx, c, productID, variantID)
		name := asStr(row["name"])
		if t := asStr(row["variant_title"]); t != "" {
			name += " — " + t
		}
		line := CartLine{
			ItemID: asInt(row["id"]), ProductID: productID, VariantID: variantID,
			Slug: asStr(row["slug"]), Name: name, NameEn: asStr(row["name_en"]),
			Thumbnail: asStr(row["thumbnail"]), SKU: asStr(row["variant_sku"]),
			Quantity: qty, UnitPrice: price, LineTotal: price * qty, Stock: stock,
			VendorID: asInt(row["vendor_id"]), TaxClass: asStr(row["tax_class"]),
			WeightG:    asInt(row["line_weight"]) * qty,
			OutOfStock: stock >= 0 && qty > stock,
		}
		sum.Items = append(sum.Items, line)
		sum.Subtotal += line.LineTotal
		sum.ItemCount += qty
		sum.TotalWeightG += line.WeightG
	}

	if sum.Coupon != "" {
		amount, kind, err := ValidateCoupon(ctx, c, sum.Coupon, sum.Subtotal, asInt(head["user_id"]))
		if err != nil {

			sum.CouponMessage = err.Error()
			sum.Coupon = ""
		} else {
			sum.Discount = amount
			sum.CouponApplied = true
			sum.CouponKind = kind
			if kind == "free_shipping" {
				sum.FreeShipping = true
			}
		}
	}

	sum.Tax = CalculateTax(ctx, c, sum.Subtotal-sum.Discount, "IR", "")

	if shippingMethodID > 0 && !sum.FreeShipping {
		sum.Shipping = ShippingCost(ctx, c, shippingMethodID, sum.Subtotal-sum.Discount, sum.TotalWeightG)
	}
	freeOver := c.SettingInt("free_shipping_over", 0)
	if freeOver > 0 && sum.Subtotal >= freeOver {
		sum.Shipping = 0
		sum.FreeShipping = true
	}

	sum.GrandTotal = sum.Subtotal - sum.Discount + sum.Tax + sum.Shipping
	if sum.GrandTotal < 0 {
		sum.GrandTotal = 0
	}
	return sum, nil
}
