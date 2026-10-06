package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

var loyaltyTiers = []struct {
	Name string
	Min  int
	Perk string
}{
	{"bronze", 0, "1x points"},
	{"silver", 5000, "1.25x points + free shipping over threshold"},
	{"gold", 20000, "1.5x points + priority support"},
	{"platinum", 60000, "2x points + exclusive flash sales"},
}

func registerLoyaltyModule(rt *Router, c *core.Core) {
	rt.Module("27-loyalty")

	rt.Handle(http.MethodGet, "/loyalty/rules", "", func(w http.ResponseWriter, r *http.Request) {
		tiers := make([]map[string]any, 0, len(loyaltyTiers))
		for _, t := range loyaltyTiers {
			tiers = append(tiers, map[string]any{"tier": t.Name, "minLifetimePoints": t.Min, "perk": t.Perk})
		}
		writeOK(w, map[string]any{
			"earnRatePerCurrencyUnit": c.SettingInt("loyalty_points_per_unit", 10000),
			"redeemValuePerPoint":     c.SettingInt("loyalty_value_per_point", 1000),
			"minRedeemPoints":         c.SettingInt("loyalty_min_redeem", 50),
			"pointsExpireAfterDays":   c.SettingInt("loyalty_expiry_days", 365),
			"tiers":                   tiers,
		})
	})

	rt.Handle(http.MethodGet, "/loyalty/me", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO loyalty_accounts(user_id) VALUES(?)`, uid)
		acc, _ := c.DB.MapOne(r.Context(), `SELECT * FROM loyalty_accounts WHERE user_id=?`, uid)
		txs, _ := c.DB.Maps(r.Context(),
			`SELECT points,reason,expires_at,created_at FROM loyalty_transactions WHERE user_id=? ORDER BY id DESC LIMIT 30`, uid)
		next := ""
		for _, t := range loyaltyTiers {
			if asInt(acc["lifetime"]) < t.Min {
				next = t.Name
				break
			}
		}
		writeOK(w, map[string]any{"account": acc, "transactions": txs, "nextTier": next})
	})

	rt.Handle(http.MethodPost, "/loyalty/redeem", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		points := iVal(m, "points", 0)
		minRedeem := c.SettingInt("loyalty_min_redeem", 50)
		if points < minRedeem {
			httpx.Fail(w, httpx.CodeValidation, "Minimum redemption is "+toString(minRedeem)+" points")
			return
		}
		balance, _ := c.DB.Count(r.Context(), `SELECT COALESCE(points,0) FROM loyalty_accounts WHERE user_id=?`, uid)
		if balance < points {
			httpx.Fail(w, httpx.CodeValidation, "Insufficient points")
			return
		}
		value := points * c.SettingInt("loyalty_value_per_point", 1000)
		if _, err := c.DB.Write(r.Context(),
			`UPDATE loyalty_accounts SET points = points - ?, updated_at=datetime('now') WHERE user_id=?`, points, uid); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		_, _ = c.DB.Write(r.Context(), `INSERT INTO loyalty_transactions(user_id,points,reason) VALUES(?,?,'redeem')`, uid, -points)
		if _, err := WalletCredit(r.Context(), c, uid, value, "loyalty_redeem", "points:"+toString(points)); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "loyalty.redeem", "loyalty_accounts", toString(uid), map[string]any{"points": points, "value": value})
		httpx.Success(w, http.StatusOK, "Points redeemed to wallet",
			map[string]any{"points": points, "walletCredit": value}, nil)
	})

	rt.Handle(http.MethodGet, "/gift-cards", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(),
			`SELECT id,code,initial_amount,balance,currency,status,expires_at,created_at
			 FROM gift_cards WHERE owner_id=? OR redeemed_by=? ORDER BY id DESC`,
			middleware.UserID(r.Context()), middleware.UserID(r.Context()))
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/gift-cards/purchase", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		if amount < c.SettingInt("giftcard_min_amount", 100000) {
			httpx.Fail(w, httpx.CodeValidation, "Amount is below the minimum gift-card value")
			return
		}
		if _, err := WalletDebit(r.Context(), c, uid, amount, "purchase", "gift-card"); err != nil {
			httpx.Fail(w, httpx.CodePaymentFailed, err.Error())
			return
		}
		code := "GC-" + strings.ToUpper(randHex(6))
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO gift_cards(code,initial_amount,balance,currency,owner_id,expires_at,status)
			 VALUES(?,?,?,?,?,datetime('now','+1 year'),'active')`,
			code, amount, amount, c.Cfg.DefaultCurrency, uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		audit(c, r, "giftcard.purchase", "gift_cards", toString(id), map[string]any{"amount": amount})
		writeCreated(w, map[string]any{"id": id, "code": code, "amount": amount, "expiresInDays": 365})
	})

	rt.Handle(http.MethodPost, "/gift-cards/redeem", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		code := strings.ToUpper(strings.TrimSpace(sVal(m, "code")))
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM gift_cards WHERE UPPER(code)=?`, code)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Gift card not found")
			return
		}
		switch asStr(row["status"]) {
		case "redeemed":
			httpx.Fail(w, httpx.CodeGiftRedeemed, "")
			return
		case "expired":
			httpx.Fail(w, httpx.CodeValidation, "Gift card has expired")
			return
		}
		balance := asInt(row["balance"])
		if balance <= 0 {
			httpx.Fail(w, httpx.CodeGiftRedeemed, "")
			return
		}
		if _, err := WalletCredit(r.Context(), c, uid, balance, "giftcard", code); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE gift_cards SET balance=0, status='redeemed', redeemed_by=?, redeemed_at=datetime('now') WHERE id=?`,
			uid, row["id"])
		audit(c, r, "giftcard.redeem", "gift_cards", asStr(row["id"]), map[string]any{"amount": balance})
		httpx.Success(w, http.StatusOK, "Gift card redeemed to wallet", map[string]any{"credited": balance}, nil)
	})

	rt.Handle(http.MethodGet, "/referral/me", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		u, err := userRow(r.Context(), c, uid)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		refs, _ := c.DB.Maps(r.Context(), `SELECT r.id,r.status,r.reward,r.created_at,u.email AS referee
			FROM referrals r LEFT JOIN users u ON u.id=r.referee_id WHERE r.referrer_id=? ORDER BY r.id DESC`, uid)
		earned, _ := c.DB.Count(r.Context(),
			`SELECT COALESCE(SUM(reward),0) FROM referrals WHERE referrer_id=? AND status='rewarded'`, uid)
		base := strings.TrimSuffix(c.Setting("site_url", c.Cfg.BaseURL), "/")
		writeOK(w, map[string]any{
			"code": u["referral_code"], "shareUrl": base + "/register?ref=" + asStr(u["referral_code"]),
			"referrals": refs, "totalEarned": earned,
			"rewardPerSignup": c.SettingInt("referral_reward", 50000),
		})
	})

	rt.Handle(http.MethodPost, "/referral/apply", "", func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		code := strings.ToUpper(sVal(m, "code"))
		uid := iVal(m, "userId", middleware.UserID(r.Context()))
		if code == "" || uid == 0 {
			httpx.Fail(w, httpx.CodeValidation, "code and an authenticated user are required")
			return
		}
		applyReferral(r.Context(), c, code, uid)
		httpx.NoBody(w, "Referral applied")
	})

	rt.Handle(http.MethodGet, "/admin/referral/stats", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		byStatus, _ := c.DB.Maps(r.Context(),
			`SELECT status, COUNT(*) AS count, COALESCE(SUM(reward),0) AS rewards FROM referrals GROUP BY status`)
		top, _ := c.DB.Maps(r.Context(), `SELECT u.email, COUNT(r.id) AS referrals,
			COALESCE(SUM(r.reward),0) AS earned FROM referrals r JOIN users u ON u.id=r.referrer_id
			GROUP BY r.referrer_id ORDER BY referrals DESC LIMIT 20`)
		writeOK(w, map[string]any{"byStatus": byStatus, "topReferrers": top})
	})

	rt.Handle(http.MethodGet, "/admin/loyalty/accounts", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM loyalty_accounts`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT la.*, u.email FROM loyalty_accounts la
			JOIN users u ON u.id=la.user_id ORDER BY la.lifetime DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})
}

func AwardLoyaltyPoints(ctx context.Context, c *core.Core, userID, orderTotal int, reason string) {
	if userID <= 0 || orderTotal <= 0 {
		return
	}
	per := c.SettingInt("loyalty_points_per_unit", 10000)
	if per <= 0 {
		per = 10000
	}
	points := orderTotal / per
	if points <= 0 {
		return
	}
	_, _ = c.DB.Write(ctx, `INSERT OR IGNORE INTO loyalty_accounts(user_id) VALUES(?)`, userID)
	_, _ = c.DB.Write(ctx,
		`UPDATE loyalty_accounts SET points = points + ?, lifetime = lifetime + ?, updated_at=datetime('now') WHERE user_id=?`,
		points, points, userID)
	expiry := time.Now().UTC().AddDate(0, 0, c.SettingInt("loyalty_expiry_days", 365)).Format("2006-01-02 15:04:05")
	_, _ = c.DB.Write(ctx,
		`INSERT INTO loyalty_transactions(user_id,points,reason,expires_at) VALUES(?,?,?,?)`,
		userID, points, reason, expiry)

	lifetime, _ := c.DB.Count(ctx, `SELECT lifetime FROM loyalty_accounts WHERE user_id=?`, userID)
	tier := loyaltyTiers[0].Name
	for _, t := range loyaltyTiers {
		if lifetime >= t.Min {
			tier = t.Name
		}
	}
	_, _ = c.DB.Write(ctx, `UPDATE loyalty_accounts SET tier=? WHERE user_id=?`, tier, userID)
}

func applyReferral(ctx context.Context, c *core.Core, code string, newUserID int) {
	if code == "" || newUserID <= 0 {
		return
	}
	var referrerID int
	if err := c.DB.Row(ctx, `SELECT id FROM users WHERE UPPER(referral_code)=? AND deleted_at IS NULL`,
		[]any{strings.ToUpper(code)}, &referrerID); err != nil || referrerID == newUserID {
		return
	}
	if n, _ := c.DB.Count(ctx, `SELECT COUNT(*) FROM referrals WHERE referee_id=?`, newUserID); n > 0 {
		return
	}
	reward := c.SettingInt("referral_reward", 50000)
	_, _ = c.DB.Write(ctx, `UPDATE users SET referred_by=? WHERE id=?`, referrerID, newUserID)
	_, _ = c.DB.Write(ctx,
		`INSERT INTO referrals(referrer_id,referee_id,code,reward,status) VALUES(?,?,?,?,'rewarded')`,
		referrerID, newUserID, code, reward)
	_, _ = WalletCredit(ctx, c, referrerID, reward, "referral", "referee:"+toString(newUserID))
	c.Notify(ctx, referrerID, "referral", "پاداش معرفی / Referral reward",
		"پاداش دعوت دوست به کیف پول شما اضافه شد.", "/account/wallet")
}
