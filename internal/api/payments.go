package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/arena/avaluna/internal/auth"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/httpx"
	"github.com/arena/avaluna/internal/middleware"
)

type PaymentSession struct {
	Reference   string `json:"reference"`
	Gateway     string `json:"gateway"`
	Amount      int    `json:"amount"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	Authority   string `json:"authority,omitempty"`
	RedirectURL string `json:"redirectUrl,omitempty"`
	Sandbox     bool   `json:"sandbox"`
	Message     string `json:"message,omitempty"`
}

func registerPaymentsModule(rt *Router, c *core.Core) {
	rt.Module("07-payments")
	lim := rt.Limit("payment", 10, time.Minute)
	idem := middleware.Idempotency(idemStore{c}, false)

	rt.Handle(http.MethodGet, "/payments/methods", "", func(w http.ResponseWriter, r *http.Request) {
		rows, err := c.DB.Maps(r.Context(),
			`SELECT code,name,name_en,provider,fee_fixed,fee_percent FROM payment_methods WHERE active=1 ORDER BY sort_order`)
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		writeOK(w, rows)
	})

	rt.HandleWith(http.MethodPost, "/payments/create-session", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		orderID := iVal(m, "orderId", 0)
		gateway := sVal(m, "gateway")
		if orderID == 0 {
			httpx.Fail(w, httpx.CodeValidation, "orderId is required")
			return
		}
		order, err := c.DB.MapOne(r.Context(), `SELECT * FROM orders WHERE id=?`, orderID)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "Order not found")
			return
		}
		if asInt(order["user_id"]) != middleware.UserID(r.Context()) && !isStaff(r) {
			httpx.Fail(w, httpx.CodeForbidden, "")
			return
		}
		if asStr(order["payment_status"]) == "paid" {
			httpx.Fail(w, httpx.CodeConflict, "Order is already paid")
			return
		}
		if gateway == "" {
			gateway = asStr(order["payment_method"])
		}
		sess, err := CreatePaymentSession(r.Context(), c, orderID, middleware.UserID(r.Context()),
			gateway, asInt(order["grand_total"]), asStr(order["currency"]))
		if err != nil {
			httpx.Fail(w, httpx.CodePaymentFailed, err.Error())
			return
		}
		writeCreated(w, sess)
	}, lim, idem)

	rt.HandleWith(http.MethodPost, "/payments/verify", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		ref := sVal(m, "reference")
		if ref == "" {
			ref = sVal(m, "authority")
		}
		outcome := sVal(m, "outcome")
		if ref == "" {
			httpx.Fail(w, httpx.CodeValidation, "reference is required")
			return
		}
		res, err := VerifyPayment(r.Context(), c, ref, outcome)
		if err != nil {
			httpx.Fail(w, httpx.CodePaymentFailed, err.Error())
			return
		}
		audit(c, r, "payment.verify", "payments", ref, res)
		writeOK(w, res)
	}, lim, idem)

	rt.Handle(http.MethodGet, "/payments/{id}", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM payments WHERE id=? OR reference=?`, key, key)
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

	rt.HandleWith(http.MethodPost, "/payments/{id}/refund", roleAdmin, func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("id")
		row, err := c.DB.MapOne(r.Context(), `SELECT * FROM payments WHERE id=? OR reference=?`, key, key)
		if err != nil {
			httpx.Fail(w, httpx.CodeNotFound, "")
			return
		}
		if asStr(row["status"]) != "succeeded" {
			httpx.Fail(w, httpx.CodeConflict, "Only succeeded payments can be refunded")
			return
		}
		m, _ := body(r)
		amount := iVal(m, "amount", asInt(row["amount"]))
		if amount <= 0 || amount > asInt(row["amount"])-asInt(row["refunded"]) {
			httpx.Fail(w, httpx.CodeValidation, "Invalid refund amount")
			return
		}
		if err := RefundPayment(r.Context(), c, asInt(row["id"]), amount); err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "payment.refund", "payments", asStr(row["id"]), map[string]any{"amount": amount})
		httpx.Success(w, http.StatusOK, "Refund processed", map[string]any{"refunded": amount}, nil)
	}, idem)

	rt.Handle(http.MethodGet, "/payments/wallet/balance", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		uid := middleware.UserID(r.Context())
		_, _ = c.DB.Write(r.Context(), `INSERT OR IGNORE INTO wallets(user_id,balance,currency) VALUES(?,0,?)`, uid, c.Cfg.DefaultCurrency)
		row, _ := c.DB.MapOne(r.Context(), `SELECT balance,currency,updated_at FROM wallets WHERE user_id=?`, uid)
		txs, _ := c.DB.Maps(r.Context(), `SELECT amount,type,balance_after,reference,created_at FROM wallet_transactions
			WHERE user_id=? ORDER BY id DESC LIMIT 20`, uid)
		writeOK(w, map[string]any{"wallet": row, "transactions": txs})
	})

	rt.HandleWith(http.MethodPost, "/payments/wallet/topup", RoleAuthenticated, func(w http.ResponseWriter, r *http.Request) {
		m, _ := body(r)
		amount := iVal(m, "amount", 0)
		if amount <= 0 {
			httpx.Fail(w, httpx.CodeValidation, "amount must be positive")
			return
		}
		uid := middleware.UserID(r.Context())
		bal, err := WalletCredit(r.Context(), c, uid, amount, "topup", "sandbox-topup")
		if err != nil {
			httpx.Fail(w, httpx.CodeInternal, err.Error())
			return
		}
		audit(c, r, "wallet.topup", "wallets", toString(uid), map[string]any{"amount": amount})
		httpx.Success(w, http.StatusOK, "Wallet credited", map[string]any{"balance": bal}, nil)
	}, lim, idem)

	for _, gw := range []string{"stripe", "zarinpal", "idpay"} {
		gateway := gw
		rt.Handle(http.MethodPost, "/payments/webhook/"+gateway, "", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			secret := c.Setting("gateway_"+gateway+"_secret", "sandbox_secret")
			sig := r.Header.Get("X-Signature")
			if sig == "" {
				sig = r.Header.Get("Stripe-Signature")
			}
			if !c.SettingBool("webhook_allow_unsigned", true) && !auth.VerifyHMAC(secret, raw, sig) {
				httpx.Fail(w, httpx.CodeForbidden, "Invalid webhook signature")
				return
			}
			var payload map[string]any
			_ = httpx.Decode(&http.Request{Body: io.NopCloser(strings.NewReader(string(raw))), Header: r.Header}, &payload)
			ref := asStr(payload["reference"])
			if ref == "" {
				ref = asStr(payload["authority"])
			}
			if ref == "" {
				httpx.Fail(w, httpx.CodeValidation, "reference missing in webhook payload")
				return
			}
			outcome := "success"
			if s := asStr(payload["status"]); s != "" && s != "succeeded" && s != "ok" && s != "success" {
				outcome = "failure"
			}
			res, err := VerifyPayment(r.Context(), c, ref, outcome)
			if err != nil {
				httpx.Fail(w, httpx.CodePaymentFailed, err.Error())
				return
			}
			writeOK(w, res)
		})
	}
}

func CreatePaymentSession(ctx context.Context, c *core.Core, orderID, userID int,
	gateway string, amount int, currency string) (*PaymentSession, error) {
	if gateway == "" {
		gateway = "wallet"
	}
	ref := "PAY-" + strings.ToUpper(auth.RandomToken(8))
	authority := strings.ToUpper(auth.RandomToken(12))

	if gateway == "wallet" {
		if _, err := WalletDebit(ctx, c, userID, amount, "purchase", ref); err != nil {
			return nil, err
		}
		if _, err := c.DB.Write(ctx,
			`INSERT INTO payments(reference,order_id,user_id,gateway,amount,currency,status,authority)
			 VALUES(?,?,?,?,?,?,'succeeded',?)`,
			ref, nullIf(orderID), nullIf(userID), gateway, amount, currency, authority); err != nil {

			if _, rerr := WalletCredit(ctx, c, userID, amount, "refund", ref); rerr != nil {
				c.Log.Error("wallet debit could not be reversed after a failed payment insert",
					"reference", ref, "user", userID, "amount", amount, "error", rerr)
			}
			return nil, err
		}
		if err := MarkOrderPaid(ctx, c, orderID); err != nil {
			return nil, err
		}
		return &PaymentSession{Reference: ref, Gateway: gateway, Amount: amount, Currency: currency,
			Status: "succeeded", Authority: authority, Sandbox: true, Message: "Paid from wallet"}, nil
	}

	if _, err := c.DB.Write(ctx,
		`INSERT INTO payments(reference,order_id,user_id,gateway,amount,currency,status,authority,meta_json)
		 VALUES(?,?,?,?,?,?,'pending',?,?)`,
		ref, nullIf(orderID), nullIf(userID), gateway, amount, currency, authority,
		jsonStr(map[string]any{"sandbox": true, "createdAt": time.Now().UTC()})); err != nil {
		return nil, err
	}
	redirect := c.Cfg.APIPrefix + "/payments/sandbox/" + ref
	return &PaymentSession{
		Reference: ref, Gateway: gateway, Amount: amount, Currency: currency,
		Status: "pending", Authority: authority, Sandbox: true,
		RedirectURL: redirect,
		Message:     "Sandbox gateway — POST /payments/verify with {reference, outcome:'success'} to settle.",
	}, nil
}

func VerifyPayment(ctx context.Context, c *core.Core, reference, outcome string) (map[string]any, error) {
	row, err := c.DB.MapOne(ctx, `SELECT * FROM payments WHERE reference=? OR authority=?`, reference, reference)
	if err != nil {
		return nil, errors.New("payment not found")
	}
	if asStr(row["status"]) == "succeeded" {
		return map[string]any{"reference": row["reference"], "status": "succeeded", "replay": true}, nil
	}
	if outcome == "failure" || outcome == "failed" {
		_, _ = c.DB.Write(ctx, `UPDATE payments SET status='failed', updated_at=datetime('now') WHERE id=?`, row["id"])
		if oid := asInt(row["order_id"]); oid > 0 {
			_, _ = c.DB.Write(ctx, `UPDATE orders SET payment_status='failed' WHERE id=?`, oid)
			_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'payment_failed','Gateway declined')`, oid)
		}
		return nil, errors.New("payment declined by gateway")
	}
	cardMask := "**** **** **** " + strings.ToUpper(auth.RandomToken(2))[:4]
	if _, err := c.DB.Write(ctx,
		`UPDATE payments SET status='succeeded', card_mask=?, updated_at=datetime('now') WHERE id=?`,
		cardMask, row["id"]); err != nil {
		return nil, err
	}
	if oid := asInt(row["order_id"]); oid > 0 {
		if err := MarkOrderPaid(ctx, c, oid); err != nil {
			return nil, err
		}
	}
	DispatchWebhook(ctx, c, "payment.succeeded", map[string]any{
		"reference": row["reference"], "amount": row["amount"], "gateway": row["gateway"],
	})
	return map[string]any{
		"reference": row["reference"], "status": "succeeded",
		"amount": row["amount"], "cardMask": cardMask, "orderId": row["order_id"],
	}, nil
}

func RefundPayment(ctx context.Context, c *core.Core, paymentID, amount int) error {
	row, err := c.DB.MapOne(ctx, `SELECT * FROM payments WHERE id=?`, paymentID)
	if err != nil {
		return err
	}
	newRefunded := asInt(row["refunded"]) + amount
	status := "succeeded"
	if newRefunded >= asInt(row["amount"]) {
		status = "refunded"
	}
	if _, err := c.DB.Write(ctx,
		`UPDATE payments SET refunded=?, status=?, updated_at=datetime('now') WHERE id=?`,
		newRefunded, status, paymentID); err != nil {
		return err
	}
	if uid := asInt(row["user_id"]); uid > 0 {
		if _, err := WalletCredit(ctx, c, uid, amount, "refund", asStr(row["reference"])); err != nil {
			return err
		}
	}
	if oid := asInt(row["order_id"]); oid > 0 {
		ps := "partially_refunded"
		if status == "refunded" {
			ps = "refunded"
		}
		_, _ = c.DB.Write(ctx, `UPDATE orders SET payment_status=?, updated_at=datetime('now') WHERE id=?`, ps, oid)
		_, _ = c.DB.Write(ctx, `INSERT INTO order_events(order_id,status,note) VALUES(?,'refunded',?)`,
			oid, "Refunded "+toString(amount))
	}
	DispatchWebhook(ctx, c, "payment.refunded", map[string]any{"paymentId": paymentID, "amount": amount})
	return nil
}

var ErrInsufficientFunds = errors.New("insufficient wallet balance")

func WalletBalance(ctx context.Context, c *core.Core, userID int) int {
	n, _ := c.DB.Count(ctx, `SELECT COALESCE(balance,0) FROM wallets WHERE user_id=?`, userID)
	return n
}

func WalletCredit(ctx context.Context, c *core.Core, userID, amount int, kind, ref string) (int, error) {
	if userID <= 0 {
		return 0, errors.New("invalid user")
	}
	if amount < 0 {
		return 0, errors.New("amount must not be negative")
	}
	_, _ = c.DB.Write(ctx, `INSERT OR IGNORE INTO wallets(user_id,balance,currency) VALUES(?,0,?)`, userID, c.Cfg.DefaultCurrency)
	if _, err := c.DB.Write(ctx,
		`UPDATE wallets SET balance = balance + ?, updated_at=datetime('now') WHERE user_id=?`, amount, userID); err != nil {
		return 0, err
	}
	bal := WalletBalance(ctx, c, userID)
	_, _ = c.DB.Write(ctx,
		`INSERT INTO wallet_transactions(user_id,amount,type,balance_after,reference) VALUES(?,?,?,?,?)`,
		userID, amount, kind, bal, ref)
	return bal, nil
}

func WalletDebit(ctx context.Context, c *core.Core, userID, amount int, kind, ref string) (int, error) {
	if userID <= 0 {
		return 0, errors.New("invalid user")
	}
	if amount < 0 {
		return 0, errors.New("amount must not be negative")
	}
	res, err := c.DB.Write(ctx,
		`UPDATE wallets SET balance = balance - ?, updated_at=datetime('now')
		 WHERE user_id = ? AND balance >= ?`, amount, userID, amount)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrInsufficientFunds
	}
	bal := WalletBalance(ctx, c, userID)
	_, _ = c.DB.Write(ctx,
		`INSERT INTO wallet_transactions(user_id,amount,type,balance_after,reference) VALUES(?,?,?,?,?)`,
		userID, -amount, kind, bal, ref)
	return bal, nil
}
