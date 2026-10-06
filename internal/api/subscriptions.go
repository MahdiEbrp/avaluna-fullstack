package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

func registerSubsModule(rt *Router, c *core.Core) {
	rt.Module("28-subscriptions")

	plans := &Resource{
		Name: "subscription-plans", Table: "subscription_plans",
		Fields: []Field{
			{Name: "code", Kind: KindString, Required: true},
			{Name: "name", Kind: KindString, Required: true},
			{Name: "name_en", Kind: KindString},
			{Name: "description", Kind: KindString},
			{Name: "price", Kind: KindInt, Required: true},
			{Name: "currency", Kind: KindString, Default: "IRR"},
			{Name: "interval", Kind: KindString, Default: "monthly"},
			{Name: "trial_days", Kind: KindInt, Default: 0},
			{Name: "features_json", Kind: KindJSON, Default: "[]"},
			{Name: "active", Kind: KindBool, Default: true},
		},
		Filters: []string{"interval", "active"}, OrderBy: "price ASC",
		ReadRole: "", WriteRole: roleAdmin,
	}
	plans.Mount(rt, c.DB)

	rt.Handle(http.MethodGet, "/subscriptions", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		rows, _ := c.DB.Maps(r.Context(), `SELECT s.*, p.name AS plan_name, p.name_en AS plan_name_en,
			p.price, p.interval FROM subscriptions s JOIN subscription_plans p ON p.id=s.plan_id
			WHERE s.user_id=? ORDER BY s.id DESC`, middleware.UserID(r.Context()))
		writeOK(w, rows)
	})

	rt.Handle(http.MethodPost, "/subscriptions", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		m, _ := body(r)
		planID := iVal(m, "planId", 0)
		plan, err := c.DB.MapOne(r.Context(), `SELECT * FROM subscription_plans WHERE id=? AND active=1`, planID)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Plan not found")
			return
		}
		if n, _ := c.DB.Count(r.Context(),
			`SELECT COUNT(*) FROM subscriptions WHERE user_id=? AND plan_id=? AND status IN ('active','past_due')`,
			uid, planID); n > 0 {
			httpx.Fail(w, httpx.CodeConflict, "You already have an active subscription to this plan")
			return
		}
		trial := asInt(plan["trial_days"])
		price := asInt(plan["price"])
		status := "active"
		if trial == 0 && price > 0 {
			if _, err := WalletDebit(r.Context(), c, uid, price, "subscription", asStr(plan["code"])); err != nil {
				httpx.Fail(w, httpx.CodeSubPaymentFailed, err.Error())
				return
			}
		}
		renews := nextRenewal(time.Now().UTC(), asStr(plan["interval"]), trial)
		res, err := c.DB.Write(r.Context(),
			`INSERT INTO subscriptions(user_id,plan_id,status,renews_at) VALUES(?,?,?,?)`,
			uid, planID, status, renews.Format("2006-01-02 15:04:05"))
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		id, _ := res.LastInsertId()
		if trial == 0 && price > 0 {
			_, _ = c.DB.Write(r.Context(),
				`INSERT INTO subscription_invoices(subscription_id,amount,status,period_start,period_end)
				 VALUES(?,?,'paid',datetime('now'),?)`, id, price, renews.Format("2006-01-02 15:04:05"))
		}
		audit(c, r, "subscription.create", "subscriptions", toString(id), map[string]any{"planId": planID})
		DispatchWebhook(r.Context(), c, "subscription.created", map[string]any{"id": id, "planId": planID, "userId": uid})
		writeCreated(w, map[string]any{"id": id, "status": status, "trialDays": trial,
			"renewsAt": renews.Format(time.RFC3339)})
	})

	rt.Handle(http.MethodGet, "/subscriptions/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		sub, ok := ownedSubscription(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		inv, _ := c.DB.Maps(r.Context(),
			`SELECT * FROM subscription_invoices WHERE subscription_id=? ORDER BY id DESC LIMIT 24`, sub["id"])
		writeOK(w, map[string]any{"subscription": sub, "invoices": inv})
	})

	rt.Handle(http.MethodPost, "/subscriptions/{id}/cancel", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		sub, ok := ownedSubscription(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE subscriptions SET status='cancelled', cancelled_at=datetime('now') WHERE id=?`, sub["id"])
		audit(c, r, "subscription.cancel", "subscriptions", asStr(sub["id"]), nil)
		DispatchWebhook(r.Context(), c, "subscription.cancelled", map[string]any{"id": sub["id"]})
		httpx.NoBody(w, "Subscription cancelled")
	})

	rt.Handle(http.MethodPost, "/subscriptions/{id}/resume", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		sub, ok := ownedSubscription(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		_, _ = c.DB.Write(r.Context(),
			`UPDATE subscriptions SET status='active', cancelled_at=NULL, failures=0 WHERE id=?`, sub["id"])
		audit(c, r, "subscription.resume", "subscriptions", asStr(sub["id"]), nil)
		httpx.NoBody(w, "Subscription resumed")
	})

	rt.Handle(http.MethodPatch, "/subscriptions/{id}/plan", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		sub, ok := ownedSubscription(c, r)
		if !ok {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		m, _ := body(r)
		newPlan, err := c.DB.MapOne(r.Context(),
			`SELECT * FROM subscription_plans WHERE id=? AND active=1`, iVal(m, "planId", 0))
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Plan not found")
			return
		}
		_, _ = c.DB.Write(r.Context(), `UPDATE subscriptions SET plan_id=? WHERE id=?`, newPlan["id"], sub["id"])
		audit(c, r, "subscription.change_plan", "subscriptions", asStr(sub["id"]),
			map[string]any{"planId": newPlan["id"]})
		httpx.Success(w, http.StatusOK, "Plan changed",
			map[string]any{"planId": newPlan["id"], "note": "Proration is applied on the next renewal invoice."}, nil)
	})

	rt.Handle(http.MethodGet, "/admin/subscriptions", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset := paginate(r)
		total, _ := c.DB.Count(r.Context(), `SELECT COUNT(*) FROM subscriptions`)
		rows, _ := c.DB.Maps(r.Context(), `SELECT s.*, u.email, p.name AS plan_name, p.price
			FROM subscriptions s JOIN users u ON u.id=s.user_id JOIN subscription_plans p ON p.id=s.plan_id
			ORDER BY s.id DESC LIMIT ? OFFSET ?`, limit, offset)
		httpx.List(w, rows, httpx.MakePagination(page, limit, total))
	})

	rt.Handle(http.MethodPost, "/admin/subscriptions/run-billing", roleSuper, func(w http.ResponseWriter, r *http.Request) {
		out, err := RunSubscriptionBilling(r.Context(), c)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "subscription.run_billing", "subscriptions", "", map[string]any{"result": out})
		httpx.Success(w, http.StatusOK, "Billing cycle executed", map[string]any{"result": out}, nil)
	})
}

func ownedSubscription(c *core.Core, r *http.Request) (map[string]any, bool) {
	id, _ := httpx.PathInt(r, "id")
	row, err := c.DB.MapOne(r.Context(), `SELECT s.*, p.name AS plan_name, p.price, p.interval
		FROM subscriptions s JOIN subscription_plans p ON p.id=s.plan_id WHERE s.id=?`, id)
	if err != nil {
		return nil, false
	}
	if !isStaff(r) && asInt(row["user_id"]) != middleware.UserID(r.Context()) {
		return nil, false
	}
	return row, true
}

func nextRenewal(from time.Time, interval string, trialDays int) time.Time {
	if trialDays > 0 {
		return from.AddDate(0, 0, trialDays)
	}
	switch interval {
	case "yearly":
		return from.AddDate(1, 0, 0)
	case "weekly":
		return from.AddDate(0, 0, 7)
	default:
		return from.AddDate(0, 1, 0)
	}
}

func RunSubscriptionBilling(ctx context.Context, c *core.Core) (string, error) {
	due, err := c.DB.Maps(ctx, `SELECT s.*, p.price, p.interval, p.code AS plan_code
		FROM subscriptions s JOIN subscription_plans p ON p.id=s.plan_id
		WHERE s.status IN ('active','past_due') AND s.renews_at IS NOT NULL
		  AND datetime(s.renews_at) <= datetime('now') LIMIT 500`)
	if err != nil {
		return "", err
	}
	maxFail := c.SettingInt("subscription_max_failures", 3)
	charged, failed, suspended := 0, 0, 0
	for _, s := range due {
		id, uid, price := asInt(s["id"]), asInt(s["user_id"]), asInt(s["price"])
		next := nextRenewal(time.Now().UTC(), asStr(s["interval"]), 0).Format("2006-01-02 15:04:05")
		if _, err := WalletDebit(ctx, c, uid, price, "subscription", asStr(s["plan_code"])); err != nil {
			failed++
			fails := asInt(s["failures"]) + 1
			status := "past_due"
			if fails >= maxFail {
				status, suspended = "expired", suspended+1
			}
			_, _ = c.DB.Write(ctx,
				`UPDATE subscriptions SET failures=?, status=?, renews_at=datetime('now','+1 day') WHERE id=?`,
				fails, status, id)
			_, _ = c.DB.Write(ctx,
				`INSERT INTO subscription_invoices(subscription_id,amount,status,period_start,period_end)
				 VALUES(?,?,'failed',datetime('now'),?)`, id, price, next)
			c.Notify(ctx, uid, "subscription", "پرداخت اشتراک ناموفق / Subscription payment failed",
				"موجودی کیف پول کافی نیست. لطفاً شارژ کنید.", "/account/subscriptions")
			DispatchWebhook(ctx, c, "subscription.payment_failed",
				map[string]any{"id": id, "attempt": fails, "status": status})
			continue
		}
		charged++
		_, _ = c.DB.Write(ctx,
			`UPDATE subscriptions SET status='active', failures=0, renews_at=? WHERE id=?`, next, id)
		_, _ = c.DB.Write(ctx,
			`INSERT INTO subscription_invoices(subscription_id,amount,status,period_start,period_end)
			 VALUES(?,?,'paid',datetime('now'),?)`, id, price, next)
		AwardLoyaltyPoints(ctx, c, uid, price, "subscription_renewal")
		c.Notify(ctx, uid, "subscription", "تمدید اشتراک / Subscription renewed",
			"اشتراک شما با موفقیت تمدید شد.", "/account/subscriptions")
		DispatchWebhook(ctx, c, "subscription.renewed", map[string]any{"id": id, "amount": price})
	}
	return fmt.Sprintf("due=%d charged=%d failed=%d suspended=%d", len(due), charged, failed, suspended), nil
}
