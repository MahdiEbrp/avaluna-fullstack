package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arena/avaluna/internal/config"
	"github.com/arena/avaluna/internal/core"
	"github.com/arena/avaluna/internal/database"
)

func svcEnv(t *testing.T) (*core.Core, context.Context) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Load()
	cfg.DatabasePath = filepath.Join(dir, "svc.db")
	cfg.BackupDir = filepath.Join(dir, "backups")
	cfg.UploadDir = filepath.Join(dir, "uploads")
	cfg.AppEnv = "test"
	cfg.EnableCron = false
	cfg.EnableQueue = false

	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	return core.New(cfg, db, log), ctx
}

func count(t *testing.T, c *core.Core, q string, args ...any) int {
	t.Helper()
	n, err := c.DB.Count(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func str(t *testing.T, c *core.Core, q string, args ...any) string {
	t.Helper()
	v, err := c.DB.Scalar(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("scalar %q: %v", q, err)
	}
	return v.String
}

func TestWalletCreditDebitKeepLedgerAndBalanceInStep(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 9

	start := WalletBalance(ctx, c, user)

	bal, err := WalletCredit(ctx, c, user, 500_000, "topup", "REF-CREDIT")
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
	if want := start + 500_000; bal != want {
		t.Errorf("balance after credit = %d, want %d", bal, want)
	}
	if got := WalletBalance(ctx, c, user); got != bal {
		t.Errorf("WalletBalance = %d but credit reported %d", got, bal)
	}

	bal2, err := WalletDebit(ctx, c, user, 200_000, "purchase", "REF-DEBIT")
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if want := bal - 200_000; bal2 != want {
		t.Errorf("balance after debit = %d, want %d", bal2, want)
	}

	amt := str(t, c, `SELECT amount FROM wallet_transactions WHERE reference='REF-DEBIT'`)
	if amt != "-200000" {
		t.Errorf("ledger amount for the debit = %q, want -200000", amt)
	}
	after := str(t, c, `SELECT balance_after FROM wallet_transactions WHERE reference='REF-DEBIT'`)
	if after != str(t, c, `SELECT balance FROM wallets WHERE user_id=?`, user) {
		t.Errorf("balance_after %q does not match the wallet", after)
	}

	delta := count(t, c,
		`SELECT COALESCE(SUM(amount),0) FROM wallet_transactions WHERE user_id=? AND reference IN ('REF-CREDIT','REF-DEBIT')`, user)
	if want := WalletBalance(ctx, c, user) - start; delta != want {
		t.Errorf("ledger delta %d does not match the balance change %d", delta, want)
	}
}

func TestWalletDebitRefusesToOverdraw(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 10

	before := WalletBalance(ctx, c, user)
	ledgerBefore := count(t, c, `SELECT COUNT(*) FROM wallet_transactions WHERE user_id=?`, user)

	if _, err := WalletDebit(ctx, c, user, before+1, "purchase", "REF-OVERDRAW"); err == nil {
		t.Fatal("debiting more than the balance was allowed")
	}
	if got := WalletBalance(ctx, c, user); got != before {
		t.Errorf("a refused debit changed the balance: %d -> %d", before, got)
	}
	if got := count(t, c, `SELECT COUNT(*) FROM wallet_transactions WHERE user_id=?`, user); got != ledgerBefore {
		t.Error("a refused debit still wrote a ledger row")
	}
}

func TestWalletCreditRejectsAnInvalidUser(t *testing.T) {
	c, ctx := svcEnv(t)
	for _, uid := range []int{0, -1} {
		if _, err := WalletCredit(ctx, c, uid, 1000, "topup", "X"); err == nil {
			t.Errorf("crediting user %d was allowed", uid)
		}
	}
}

func TestWalletCreditCreatesAMissingWallet(t *testing.T) {
	c, ctx := svcEnv(t)

	res, err := c.DB.Write(ctx,
		`INSERT INTO users(uuid,email,password_hash,role,status,first_name,last_name)
		 VALUES('svc-uuid-1','walletless@example.com','x','customer','active','No','Wallet')`)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	id64, _ := res.LastInsertId()
	uid := int(id64)

	if count(t, c, `SELECT COUNT(*) FROM wallets WHERE user_id=?`, uid) != 0 {
		t.Fatal("the fixture user already has a wallet")
	}
	bal, err := WalletCredit(ctx, c, uid, 250_000, "refund", "REF-NEW")
	if err != nil {
		t.Fatalf("credit: %v", err)
	}
	if bal != 250_000 {
		t.Errorf("balance = %d, want 250000", bal)
	}
}

func orderFor(t *testing.T, c *core.Core, userID, total int) int {
	t.Helper()
	res, err := c.DB.Write(context.Background(),
		`INSERT INTO orders(number,user_id,email,status,payment_status,currency,
		 subtotal,grand_total,placed_at)
		 VALUES(?,?,'svc@example.com','pending','unpaid','IRR',?,?,datetime('now'))`,
		"ORD-SVC-"+toString(total)+"-"+toString(userID), userID, total, total)
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func TestWalletGatewaySettlesImmediately(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 7

	if _, err := WalletCredit(ctx, c, user, 10_000_000, "topup", "FUND"); err != nil {
		t.Fatalf("fund wallet: %v", err)
	}
	before := WalletBalance(ctx, c, user)

	oid := orderFor(t, c, user, 1_500_000)
	sess, err := CreatePaymentSession(ctx, c, oid, user, "wallet", 1_500_000, "IRR")
	if err != nil {
		t.Fatalf("wallet session: %v", err)
	}
	if sess.Status != "succeeded" {
		t.Errorf("status = %q, want succeeded", sess.Status)
	}
	if sess.RedirectURL != "" {
		t.Error("a wallet payment should not need a redirect")
	}
	if !strings.HasPrefix(sess.Reference, "PAY-") {
		t.Errorf("reference = %q, want a PAY- prefix", sess.Reference)
	}
	if got := WalletBalance(ctx, c, user); got != before-1_500_000 {
		t.Errorf("balance = %d, want %d", got, before-1_500_000)
	}
	if st := str(t, c, `SELECT status FROM payments WHERE reference=?`, sess.Reference); st != "succeeded" {
		t.Errorf("stored payment status = %q", st)
	}
}

func TestWalletGatewayRefusesWhenUnderfunded(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 10

	rows := count(t, c, `SELECT COUNT(*) FROM payments`)
	oid := orderFor(t, c, user, 999_999_999)
	_, err := CreatePaymentSession(ctx, c, oid, user, "wallet", 999_999_999, "IRR")
	if err == nil {
		t.Fatal("an unaffordable wallet payment was accepted")
	}
	if !strings.Contains(err.Error(), "insufficient") {
		t.Errorf("error = %q, want it to mention the balance", err)
	}

	if got := count(t, c, `SELECT COUNT(*) FROM payments`); got != rows {
		t.Errorf("payments table grew from %d to %d on a refused payment", rows, got)
	}
}

func TestRedirectGatewayStaysPendingUntilVerified(t *testing.T) {
	c, ctx := svcEnv(t)

	oid := orderFor(t, c, 7, 450_000)
	sess, err := CreatePaymentSession(ctx, c, oid, 7, "zarinpal", 450_000, "IRR")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if sess.Status != "pending" {
		t.Errorf("status = %q, want pending", sess.Status)
	}
	if !strings.Contains(sess.RedirectURL, "/payments/sandbox/") {
		t.Errorf("redirect = %q", sess.RedirectURL)
	}
	if !sess.Sandbox {
		t.Error("the simulated gateway should be flagged as a sandbox")
	}

	out, err := VerifyPayment(ctx, c, sess.Reference, "success")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out["status"] != "succeeded" {
		t.Errorf("verify returned %v", out["status"])
	}
	if mask, _ := out["cardMask"].(string); !strings.HasPrefix(mask, "**** ") {
		t.Errorf("cardMask = %q, want a masked pan", mask)
	}

	again, err := VerifyPayment(ctx, c, sess.Reference, "success")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if again["replay"] != true {
		t.Errorf("a repeated verify was not reported as a replay: %v", again)
	}
}

func TestVerifyPaymentHandlesDeclineAndUnknownReference(t *testing.T) {
	c, ctx := svcEnv(t)

	oid := orderFor(t, c, 7, 120_000)
	sess, err := CreatePaymentSession(ctx, c, oid, 7, "stripe", 120_000, "IRR")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := VerifyPayment(ctx, c, sess.Reference, "failure"); err == nil {
		t.Fatal("a declined payment did not return an error")
	}
	if st := str(t, c, `SELECT status FROM payments WHERE reference=?`, sess.Reference); st != "failed" {
		t.Errorf("status after decline = %q, want failed", st)
	}

	if _, err := VerifyPayment(ctx, c, "PAY-DOESNOTEXIST", "success"); err == nil {
		t.Error("verifying an unknown reference succeeded")
	}
}

func TestRefundCreditsTheWalletAndTracksPartials(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 8

	if _, err := WalletCredit(ctx, c, user, 5_000_000, "topup", "FUND"); err != nil {
		t.Fatalf("fund: %v", err)
	}
	oid := orderFor(t, c, user, 1_000_000)
	sess, err := CreatePaymentSession(ctx, c, oid, user, "wallet", 1_000_000, "IRR")
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	payID := count(t, c, `SELECT id FROM payments WHERE reference=?`, sess.Reference)
	afterPay := WalletBalance(ctx, c, user)

	if err := RefundPayment(ctx, c, payID, 400_000); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if got := WalletBalance(ctx, c, user); got != afterPay+400_000 {
		t.Errorf("balance after partial refund = %d, want %d", got, afterPay+400_000)
	}
	if st := str(t, c, `SELECT status FROM payments WHERE id=?`, payID); st != "succeeded" {
		t.Errorf("status after a partial refund = %q, want it to stay succeeded", st)
	}
	if r := count(t, c, `SELECT refunded FROM payments WHERE id=?`, payID); r != 400_000 {
		t.Errorf("refunded = %d, want 400000", r)
	}

	if err := RefundPayment(ctx, c, payID, 600_000); err != nil {
		t.Fatalf("final refund: %v", err)
	}
	if st := str(t, c, `SELECT status FROM payments WHERE id=?`, payID); st != "refunded" {
		t.Errorf("status after a full refund = %q, want refunded", st)
	}
	if got := WalletBalance(ctx, c, user); got != afterPay+1_000_000 {
		t.Errorf("final balance = %d, want the whole payment back", got)
	}
}

func TestRefundRejectsAnUnknownPayment(t *testing.T) {
	c, ctx := svcEnv(t)
	if err := RefundPayment(ctx, c, 999999, 1000); err == nil {
		t.Error("refunding a payment that does not exist succeeded")
	}
}

func stockFixture(t *testing.T, c *core.Core, qty int) (variantID, warehouseID int) {
	t.Helper()
	ctx := context.Background()
	res, err := c.DB.Write(ctx,
		`INSERT INTO warehouses(code,name,city,active) VALUES('SVC-WH','Service WH','Tehran',1)`)
	if err != nil {
		t.Fatalf("warehouse: %v", err)
	}
	w64, _ := res.LastInsertId()
	warehouseID = int(w64)

	variantID = count(t, c, `SELECT id FROM product_variants ORDER BY id LIMIT 1`)
	if variantID == 0 {
		t.Fatal("the seed has no product variants")
	}

	if _, err := c.DB.Write(ctx, `DELETE FROM inventory WHERE variant_id=?`, variantID); err != nil {
		t.Fatalf("clear inventory: %v", err)
	}
	if _, err := c.DB.Write(ctx,
		`INSERT INTO inventory(variant_id,warehouse_id,quantity,reserved) VALUES(?,?,?,0)`,
		variantID, warehouseID, qty); err != nil {
		t.Fatalf("inventory: %v", err)
	}
	return variantID, warehouseID
}

func TestReserveAndReleaseStock(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, warehouseID := stockFixture(t, c, 50)

	avail, err := availableAt(ctx, c, variantID, warehouseID)
	if err != nil {
		t.Fatalf("availableAt: %v", err)
	}
	if avail != 50 {
		t.Fatalf("available = %d, want 50", avail)
	}

	if err := ReserveStock(ctx, c, variantID, 20, "ORD-SVC-1"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if got, _ := availableAt(ctx, c, variantID, warehouseID); got != 30 {
		t.Errorf("available after reserving 20 = %d, want 30", got)
	}

	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 50 {
		t.Errorf("quantity = %d, a reservation should not reduce it", q)
	}

	if err := ReleaseStock(ctx, c, variantID, 5, false, "ORD-SVC-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if got, _ := availableAt(ctx, c, variantID, warehouseID); got != 35 {
		t.Errorf("available after releasing 5 = %d, want 35", got)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 50 {
		t.Errorf("an uncommitted release changed quantity to %d", q)
	}

	if err := ReleaseStock(ctx, c, variantID, 15, true, "ORD-SVC-1"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 35 {
		t.Errorf("quantity after committing 15 = %d, want 35", q)
	}
	if r := count(t, c, `SELECT reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); r != 0 {
		t.Errorf("reserved = %d, want 0", r)
	}

	if n := count(t, c,
		`SELECT COUNT(*) FROM inventory_history WHERE reference='ORD-SVC-1'`); n < 3 {
		t.Errorf("history rows = %d, want one per movement", n)
	}
	if n := count(t, c,
		`SELECT COUNT(*) FROM inventory_history WHERE reference='ORD-SVC-1' AND reason='fulfil'`); n != 1 {
		t.Error("the committing release was not recorded as a fulfilment")
	}
}

func TestReserveStockReportsAShortfall(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, warehouseID := stockFixture(t, c, 3)

	err := ReserveStock(ctx, c, variantID, 10, "ORD-SHORT")
	if err == nil {
		t.Fatal("reserving more than the available stock reported success")
	}
	if !errors.Is(err, ErrInsufficientStock) {
		t.Errorf("error %v should match ErrInsufficientStock", err)
	}
	if !strings.Contains(err.Error(), "short by 7") {
		t.Errorf("error = %q, want the shortfall quantified", err)
	}

	if r := count(t, c, `SELECT reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); r != 3 {
		t.Errorf("reserved = %d, want the 3 units that existed", r)
	}
}

func TestReleaseStockReportsAMismatch(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, warehouseID := stockFixture(t, c, 20)

	if err := ReserveStock(ctx, c, variantID, 5, "ORD-MM"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	err := ReleaseStock(ctx, c, variantID, 12, true, "ORD-MM")
	if err == nil {
		t.Fatal("releasing more than was reserved reported success")
	}
	if !errors.Is(err, ErrReservationMismatch) {
		t.Errorf("error %v should match ErrReservationMismatch", err)
	}

	if r := count(t, c, `SELECT reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); r != 0 {
		t.Errorf("reserved = %d, want 0", r)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 15 {
		t.Errorf("quantity = %d, want only the 5 reserved units consumed", q)
	}
}

func TestReserveStockDrainsTheFullestWarehouseFirst(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, firstWH := stockFixture(t, c, 4)

	res, err := c.DB.Write(ctx,
		`INSERT INTO warehouses(code,name,city,active) VALUES('SVC-WH2','Second WH','Shiraz',1)`)
	if err != nil {
		t.Fatalf("warehouse: %v", err)
	}
	w64, _ := res.LastInsertId()
	secondWH := int(w64)
	if _, err := c.DB.Write(ctx,
		`INSERT INTO inventory(variant_id,warehouse_id,quantity,reserved) VALUES(?,?,30,0)`,
		variantID, secondWH); err != nil {
		t.Fatalf("inventory: %v", err)
	}

	if err := ReserveStock(ctx, c, variantID, 10, "ORD-FIFO"); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if r := count(t, c, `SELECT reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, secondWH); r != 10 {
		t.Errorf("large warehouse reserved %d, want 10", r)
	}
	if r := count(t, c, `SELECT reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, firstWH); r != 0 {
		t.Errorf("small warehouse reserved %d, want 0", r)
	}
}

func TestSetAndAdjustStockRecordHistory(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, warehouseID := stockFixture(t, c, 10)

	if err := setStock(ctx, c, variantID, warehouseID, 40, "stock_take", 1); err != nil {
		t.Fatalf("setStock: %v", err)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 40 {
		t.Errorf("quantity = %d, want 40", q)
	}

	if d := count(t, c,
		`SELECT delta FROM inventory_history WHERE reason='stock_take' ORDER BY id DESC LIMIT 1`); d != 30 {
		t.Errorf("recorded delta = %d, want 30", d)
	}

	if err := adjustStock(ctx, c, variantID, warehouseID, -15, "damage", "REF-DMG", 1); err != nil {
		t.Fatalf("adjustStock: %v", err)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 25 {
		t.Errorf("quantity after -15 = %d, want 25", q)
	}

	if err := adjustStock(ctx, c, variantID, warehouseID, -1000, "damage", "REF-BIG", 1); err != nil {
		t.Fatalf("adjustStock: %v", err)
	}
	if q := count(t, c, `SELECT quantity FROM inventory WHERE variant_id=? AND warehouse_id=?`, variantID, warehouseID); q != 0 {
		t.Errorf("quantity = %d, want it floored at 0", q)
	}
}

func TestAvailableAtIsZeroForAnUnstockedPair(t *testing.T) {
	c, ctx := svcEnv(t)
	got, err := availableAt(ctx, c, 999999, 999999)
	if err != nil {
		t.Fatalf("an unstocked pair should not error: %v", err)
	}
	if got != 0 {
		t.Errorf("available = %d, want 0", got)
	}
}

func TestShipmentLifecycleDrivesTheOrder(t *testing.T) {
	c, ctx := svcEnv(t)
	const orderID = 2

	code, err := CreateShipment(ctx, c, orderID, "")
	if err != nil {
		t.Fatalf("CreateShipment: %v", err)
	}
	if !strings.HasPrefix(code, "TRK") {
		t.Errorf("tracking code = %q", code)
	}

	if carrier := str(t, c, `SELECT carrier FROM shipments WHERE tracking_code=?`, code); carrier != "post" {
		t.Errorf("carrier = %q, want the post default", carrier)
	}
	if st := str(t, c, `SELECT status FROM orders WHERE id=?`, orderID); st != "shipped" {
		t.Errorf("order status = %q, want shipped", st)
	}
	if tc := str(t, c, `SELECT tracking_code FROM orders WHERE id=?`, orderID); tc != code {
		t.Error("the tracking code was not copied onto the order")
	}

	if err := UpdateShipment(ctx, c, code, "in_transit", "post", "Left the depot"); err != nil {
		t.Fatalf("UpdateShipment: %v", err)
	}
	if st := str(t, c, `SELECT status FROM orders WHERE id=?`, orderID); st != "shipped" {
		t.Errorf("an in-transit scan changed the order status to %q", st)
	}

	if err := UpdateShipment(ctx, c, code, "delivered", "post", "Signed for"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if st := str(t, c, `SELECT status FROM orders WHERE id=?`, orderID); st != "delivered" {
		t.Errorf("order status = %q, want delivered", st)
	}
	if d := str(t, c, `SELECT COALESCE(delivered_at,'') FROM shipments WHERE tracking_code=?`, code); d == "" {
		t.Error("delivered_at was not stamped")
	}

	events := str(t, c, `SELECT events_json FROM shipments WHERE tracking_code=?`, code)
	for _, want := range []string{"label_created", "in_transit", "delivered", "Signed for"} {
		if !strings.Contains(events, want) {
			t.Errorf("event %q missing from the trail: %s", want, events)
		}
	}
}

func TestUpdateShipmentRejectsAnUnknownCode(t *testing.T) {
	c, ctx := svcEnv(t)
	if err := UpdateShipment(ctx, c, "TRKNOPE", "delivered", "post", ""); err == nil {
		t.Error("updating a shipment that does not exist succeeded")
	}
}

func TestShippingCostRules(t *testing.T) {
	c, ctx := svcEnv(t)
	ctxW, err := c.DB.Write(ctx,
		`INSERT INTO shipping_methods(code,name,base_cost,per_kg_cost,free_over,active)
		 VALUES('svc-test','Service Test',50000,10000,2000000,1)`)
	if err != nil {
		t.Fatalf("method: %v", err)
	}
	id64, _ := ctxW.LastInsertId()
	id := int(id64)

	cases := []struct {
		name              string
		subtotal, weightG int
		want              int
	}{

		{"base only, under 1kg", 100_000, 500, 50_000},
		{"exactly 1kg is still base only", 100_000, 1000, 50_000},
		{"3kg adds two surcharges", 100_000, 3000, 50_000 + 20_000},
		{"2.1kg rounds up to 3kg", 100_000, 2100, 50_000 + 20_000},
		{"zero weight bills one kg", 100_000, 0, 50_000},
		{"free over the threshold", 2_500_000, 3000, 0},
		{"exactly at the threshold is free", 2_000_000, 1000, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShippingCost(ctx, c, id, tc.subtotal, tc.weightG); got != tc.want {
				t.Errorf("ShippingCost = %d, want %d", got, tc.want)
			}
		})
	}

	if got := ShippingCost(ctx, c, 999999, 100_000, 1000); got != 0 {
		t.Errorf("unknown method cost = %d, want 0", got)
	}
	if _, err := c.DB.Write(ctx, `UPDATE shipping_methods SET active=0 WHERE id=?`, id); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := ShippingCost(ctx, c, id, 100_000, 1000); got != 0 {
		t.Errorf("disabled method cost = %d, want 0", got)
	}
}

func TestNextRenewalHonoursIntervalAndTrial(t *testing.T) {
	base := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		interval  string
		trialDays int
		want      time.Time
	}{
		{"weekly", "weekly", 0, base.AddDate(0, 0, 7)},
		{"yearly", "yearly", 0, base.AddDate(1, 0, 0)},
		{"monthly", "monthly", 0, base.AddDate(0, 1, 0)},

		{"unknown falls back to monthly", "fortnightly", 0, base.AddDate(0, 1, 0)},
		{"empty falls back to monthly", "", 0, base.AddDate(0, 1, 0)},

		{"trial wins over monthly", "monthly", 14, base.AddDate(0, 0, 14)},
		{"trial wins over yearly", "yearly", 30, base.AddDate(0, 0, 30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := nextRenewal(base, tc.interval, tc.trialDays); !got.Equal(tc.want) {
				t.Errorf("nextRenewal(%q, trial=%d) = %v, want %v", tc.interval, tc.trialDays, got, tc.want)
			}
		})
	}

	for _, iv := range []string{"weekly", "monthly", "yearly", "nonsense"} {
		if got := nextRenewal(base, iv, 0); !got.After(base) {
			t.Errorf("interval %q produced %v, which is not after %v", iv, got, base)
		}
	}
}

func planFixture(t *testing.T, c *core.Core, userID, price int, interval, dueMod, status string) (planID, subID int) {
	t.Helper()
	ctx := context.Background()
	code := "svc-plan-" + toString(price) + "-" + toString(userID) + "-" + status
	res, err := c.DB.Write(ctx,
		`INSERT INTO subscription_plans(code,name,price,currency,interval,active)
		 VALUES(?,?,?,'IRR',?,1)`, code, "Service Plan", price, interval)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	p64, _ := res.LastInsertId()
	planID = int(p64)

	res, err = c.DB.Write(ctx,
		`INSERT INTO subscriptions(user_id,plan_id,status,renews_at,failures)
		 VALUES(?,?,?,datetime('now',?),0)`, userID, planID, status, dueMod)
	if err != nil {
		t.Fatalf("subscription: %v", err)
	}
	s64, _ := res.LastInsertId()
	return planID, int(s64)
}

func TestSubscriptionBillingChargesTheWalletAndAdvances(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 7

	if _, err := WalletCredit(ctx, c, user, 20_000_000, "topup", "FUND-SUB"); err != nil {
		t.Fatalf("fund: %v", err)
	}
	_, subID := planFixture(t, c, user, 900_000, "monthly", "-1 day", "active")
	before := WalletBalance(ctx, c, user)

	out, err := RunSubscriptionBilling(ctx, c)
	if err != nil {
		t.Fatalf("billing: %v", err)
	}
	if !strings.Contains(out, "charged=") {
		t.Errorf("summary = %q, want a charged count", out)
	}
	if got := WalletBalance(ctx, c, user); got != before-900_000 {
		t.Errorf("balance = %d, want %d after one renewal", got, before-900_000)
	}

	future := str(t, c,
		`SELECT CASE WHEN datetime(renews_at) > datetime('now') THEN 'yes' ELSE 'no' END
		 FROM subscriptions WHERE id=?`, subID)
	if future != "yes" {
		t.Error("renews_at was not advanced past now")
	}
	if st := str(t, c, `SELECT status FROM subscriptions WHERE id=?`, subID); st != "active" {
		t.Errorf("status = %q, want active", st)
	}
	if f := count(t, c, `SELECT failures FROM subscriptions WHERE id=?`, subID); f != 0 {
		t.Errorf("failures = %d, want 0 after a successful charge", f)
	}

	if n := count(t, c,
		`SELECT COUNT(*) FROM subscription_invoices WHERE subscription_id=? AND status='paid'`, subID); n != 1 {
		t.Errorf("paid invoices = %d, want 1", n)
	}

	balAfter := WalletBalance(ctx, c, user)
	if _, err := RunSubscriptionBilling(ctx, c); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if got := WalletBalance(ctx, c, user); got != balAfter {
		t.Errorf("a second billing run charged again: %d -> %d", balAfter, got)
	}
}

func TestSubscriptionBillingEscalatesRepeatedFailures(t *testing.T) {
	c, ctx := svcEnv(t)

	res, err := c.DB.Write(ctx,
		`INSERT INTO users(uuid,email,password_hash,role,status,first_name,last_name)
		 VALUES('svc-uuid-2','brokesub@example.com','x','customer','active','Broke','Sub')`)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	id64, _ := res.LastInsertId()
	uid := int(id64)

	_, subID := planFixture(t, c, uid, 5_000_000, "monthly", "-1 day", "active")
	maxFail := c.SettingInt("subscription_max_failures", 3)

	for attempt := 1; attempt <= maxFail; attempt++ {
		if _, err := c.DB.Write(ctx,
			`UPDATE subscriptions SET renews_at=datetime('now','-1 day') WHERE id=?`, subID); err != nil {
			t.Fatalf("make due: %v", err)
		}
		if _, err := RunSubscriptionBilling(ctx, c); err != nil {
			t.Fatalf("run %d: %v", attempt, err)
		}
		if f := count(t, c, `SELECT failures FROM subscriptions WHERE id=?`, subID); f != attempt {
			t.Errorf("after run %d failures = %d, want %d", attempt, f, attempt)
		}
		st := str(t, c, `SELECT status FROM subscriptions WHERE id=?`, subID)
		want := "past_due"
		if attempt >= maxFail {
			want = "expired"
		}
		if st != want {
			t.Errorf("after run %d status = %q, want %q", attempt, st, want)
		}
	}

	if WalletBalance(ctx, c, uid) < 0 {
		t.Error("a failed renewal drove the wallet negative")
	}
	if n := count(t, c,
		`SELECT COUNT(*) FROM subscription_invoices WHERE subscription_id=? AND status='failed'`, subID); n != maxFail {
		t.Errorf("failed invoices = %d, want %d", n, maxFail)
	}

	if _, err := c.DB.Write(ctx,
		`UPDATE subscriptions SET renews_at=datetime('now','-1 day') WHERE id=?`, subID); err != nil {
		t.Fatalf("make due: %v", err)
	}
	if _, err := RunSubscriptionBilling(ctx, c); err != nil {
		t.Fatalf("post-expiry run: %v", err)
	}
	if f := count(t, c, `SELECT failures FROM subscriptions WHERE id=?`, subID); f != maxFail {
		t.Errorf("an expired subscription was retried: failures = %d", f)
	}
}

func TestSubscriptionBillingSkipsWhatIsNotDue(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 11

	if _, err := WalletCredit(ctx, c, user, 10_000_000, "topup", "FUND-FUTURE"); err != nil {
		t.Fatalf("fund: %v", err)
	}

	planFixture(t, c, user, 700_000, "monthly", "+10 day", "active")
	planFixture(t, c, user, 700_000, "monthly", "-5 day", "cancelled")
	before := WalletBalance(ctx, c, user)

	out, err := RunSubscriptionBilling(ctx, c)
	if err != nil {
		t.Fatalf("billing: %v", err)
	}
	if got := WalletBalance(ctx, c, user); got != before {
		t.Errorf("balance changed from %d to %d; something not due was billed (%s)", before, got, out)
	}
}
