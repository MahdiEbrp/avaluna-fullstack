package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerDiscountsModule(rt *Router, c *core.Core) {
	rt.Module("09-discounts")

	coupons := &Resource{
		Name: "discounts/coupons", Table: "coupons",
		Fields: []Field{
			{Name: "code", Kind: KindString, Required: true},
			{Name: "type", Kind: KindString, Default: "percent"},
			{Name: "value", Kind: KindInt, Required: true},
			{Name: "min_subtotal", Kind: KindInt},
			{Name: "max_discount", Kind: KindInt},
			{Name: "usage_limit", Kind: KindInt},
			{Name: "per_user_limit", Kind: KindInt, Default: 1},
			{Name: "campaign_id", Kind: KindInt},
			{Name: "starts_at", Kind: KindString},
			{Name: "expires_at", Kind: KindString},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Searchable: []string{"code"}, Filters: []string{"active", "type", "campaign_id"},
		OrderBy: "id DESC", ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	coupons.Mount(rt, c.DB)

	campaigns := &Resource{
		Name: "discounts/campaigns", Table: "campaigns",
		Fields: []Field{
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "type", Kind: KindString, Default: "flash_sale"},
			{Name: "discount_percent", Kind: KindInt},
			{Name: "product_ids", Kind: KindJSON, Default: "[]"},
			{Name: "starts_at", Kind: KindString},
			{Name: "ends_at", Kind: KindString},
			{Name: "active", Kind: KindBool, Default: 1},
		},
		Filters: []string{"active", "type"}, OrderBy: "id DESC",
		ReadRole: roleAdmin, WriteRole: roleAdmin,
	}
	campaigns.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/discounts/flash-sales", "", func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(), `
			SELECT id,name,name_en,discount_percent,product_ids,starts_at,ends_at FROM campaigns
			WHERE active=1 AND type='flash_sale'
			  AND (starts_at IS NULL OR starts_at <= datetime('now'))
			  AND (ends_at IS NULL OR ends_at >= datetime('now'))
			ORDER BY ends_at ASC`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/discounts/validate", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		code := strings.ToUpper(sVal(m, "code"))
		subtotal := iVal(m, "subtotal", 0)
		if subtotal == 0 {
			cart, _ := EnsureCart(r.Context(), c, w, r)
			if sum, err := PriceCart(r.Context(), c, cart, 0); err == nil {
				subtotal = sum.Subtotal
			}
		}
		amount, kind, err := ValidateCoupon(r.Context(), c, code, subtotal, middleware.UserID(r.Context()))
		if err != nil {
			httpx.Fail(w, httpx.CodeCouponInvalid, err.Error())
			return
		}
		writeOK(w, map[string]any{"valid": true, "code": code, "discount": amount, "kind": kind,
			"subtotalAfter": subtotal - amount})
	})
}

func ValidateCoupon(ctx context.Context, c *core.Core, code string, subtotal, userID int) (int, string, error) {
	if code == "" {
		return 0, "", errors.New("coupon code is required")
	}
	row, err := c.DB.MapOne(ctx, `SELECT * FROM coupons WHERE UPPER(code)=UPPER(?)`, code)
	if err != nil {
		return 0, "", errors.New("coupon not found")
	}
	if asInt(row["active"]) != 1 {
		return 0, "", errors.New("coupon is disabled")
	}
	now := nowUTC()
	if s := asStr(row["starts_at"]); s != "" && s > now {
		return 0, "", errors.New("coupon is not active yet")
	}
	if e := asStr(row["expires_at"]); e != "" && e < now {
		return 0, "", errors.New("coupon has expired")
	}
	if lim := asInt(row["usage_limit"]); lim > 0 && asInt(row["used_count"]) >= lim {
		return 0, "", errors.New("coupon usage limit reached")
	}
	if min := asInt(row["min_subtotal"]); min > 0 && subtotal < min {
		return 0, "", errors.New("cart subtotal is below the coupon minimum")
	}
	if per := asInt(row["per_user_limit"]); per > 0 && userID > 0 {
		used, _ := c.DB.Count(ctx,
			`SELECT COUNT(*) FROM coupon_redemptions WHERE coupon_id=? AND user_id=?`, row["id"], userID)
		if used >= per {
			return 0, "", errors.New("you have already used this coupon")
		}
	}

	kind := asStr(row["type"])
	value := asInt(row["value"])
	var amount int
	switch kind {
	case "percent":
		amount = subtotal * value / 100
		if maxD := asInt(row["max_discount"]); maxD > 0 && amount > maxD {
			amount = maxD
		}
	case "fixed":
		amount = value
	case "free_shipping":
		return 0, "free_shipping", nil
	default:
		return 0, "", errors.New("unsupported coupon type")
	}
	if amount > subtotal {
		amount = subtotal
	}
	return amount, kind, nil
}

var ErrCouponExhausted = errors.New("coupon usage limit reached")

func RedeemCoupon(ctx context.Context, c *core.Core, code string, userID, orderID int, discount int) error {
	row, err := c.DB.MapOne(ctx,
		`SELECT id,usage_limit,per_user_limit FROM coupons WHERE UPPER(code)=UPPER(?)`, code)
	if err != nil {
		return errors.New("coupon not found")
	}
	couponID := asInt(row["id"])

	res, err := c.DB.Write(ctx,
		`UPDATE coupons SET used_count = used_count + 1
		 WHERE id = ? AND (usage_limit = 0 OR used_count < usage_limit)`, couponID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrCouponExhausted
	}

	if per := asInt(row["per_user_limit"]); per > 0 && userID > 0 {
		used, cerr := c.DB.Count(ctx,
			`SELECT COUNT(*) FROM coupon_redemptions WHERE coupon_id=? AND user_id=?`, couponID, userID)
		if cerr == nil && used >= per {
			_, _ = c.DB.Write(ctx,
				`UPDATE coupons SET used_count = MAX(0, used_count - 1) WHERE id=?`, couponID)
			return ErrCouponExhausted
		}
	}

	if _, err := c.DB.Write(ctx,
		`INSERT INTO coupon_redemptions(coupon_id,user_id,order_id,amount) VALUES(?,?,?,?)`,
		couponID, nullIf(userID), nullIf(orderID), discount); err != nil {
		_, _ = c.DB.Write(ctx, `UPDATE coupons SET used_count = MAX(0, used_count - 1) WHERE id=?`, couponID)
		return err
	}
	return nil
}

func ActiveCampaignDiscount(ctx context.Context, c *core.Core, productID int) int {
	rows, err := c.DB.Maps(ctx, `SELECT discount_percent,product_ids FROM campaigns
		WHERE active=1 AND type='flash_sale'
		  AND (starts_at IS NULL OR starts_at <= datetime('now'))
		  AND (ends_at IS NULL OR ends_at >= datetime('now'))`)
	if err != nil {
		return 0
	}
	best := 0
	needle := "" + toString(productID)
	for _, row := range rows {
		ids := asStr(row["product_ids"])
		if ids == "" || ids == "[]" || strings.Contains(ids, needle) {
			if p := asInt(row["discount_percent"]); p > best {
				best = p
			}
		}
	}
	return best
}
