package api

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func parallel(n int, fn func(i int)) {
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := 0; i < n; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			fn(i)
		}(i)
	}
	start.Done()
	done.Wait()
}

func TestStressConcurrentWalletDebitsCannotOverdraw(t *testing.T) {
	c, ctx := svcEnv(t)
	const (
		user       = 7
		unit       = 1_000_000
		affordable = 10
		attempts   = 40
	)

	if _, err := c.DB.Write(ctx,
		`INSERT INTO wallets(user_id,balance,currency) VALUES(?,?,'IRR')
		 ON CONFLICT(user_id) DO UPDATE SET balance=excluded.balance`,
		user, unit*affordable); err != nil {
		t.Fatalf("fund: %v", err)
	}

	var ok, failed atomic.Int32
	parallel(attempts, func(i int) {
		if _, err := WalletDebit(ctx, c, user, unit, "purchase", fmt.Sprintf("STRESS-%d", i)); err != nil {
			failed.Add(1)
			return
		}
		ok.Add(1)
	})

	balance := WalletBalance(ctx, c, user)
	t.Logf("succeeded=%d failed=%d final balance=%d", ok.Load(), failed.Load(), balance)

	if balance < 0 {
		t.Errorf("balance went negative (%d): the wallet was overdrawn", balance)
	}
	if got := int(ok.Load()); got > affordable {
		t.Errorf("%d debits succeeded but only %d were affordable — money was created", got, affordable)
	}

	spent := int(ok.Load()) * unit
	if want := unit*affordable - spent; balance != want {
		t.Errorf("balance = %d, want %d (start %d − spent %d)", balance, want, unit*affordable, spent)
	}

	rows, err := c.DB.Count(ctx,
		`SELECT COUNT(*) FROM wallet_transactions WHERE user_id=? AND reference LIKE 'STRESS-%'`, user)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if rows != int(ok.Load()) {
		t.Errorf("ledger holds %d rows for %d successful debits", rows, ok.Load())
	}
}

func TestStressConcurrentCreditsAreAllRecorded(t *testing.T) {
	c, ctx := svcEnv(t)
	const (
		user  = 8
		unit  = 50_000
		count = 40
	)

	if _, err := c.DB.Write(ctx,
		`INSERT INTO wallets(user_id,balance,currency) VALUES(?,0,'IRR')
		 ON CONFLICT(user_id) DO UPDATE SET balance=0`, user); err != nil {
		t.Fatalf("reset: %v", err)
	}

	parallel(count, func(i int) {
		if _, err := WalletCredit(ctx, c, user, unit, "topup", fmt.Sprintf("CREDIT-%d", i)); err != nil {
			t.Errorf("credit %d: %v", i, err)
		}
	})

	if got, want := WalletBalance(ctx, c, user), unit*count; got != want {
		t.Errorf("balance = %d, want %d — %d units were lost to a racing update",
			got, want, (want-got)/unit)
	}
}

func TestStressMixedWalletTrafficConserves(t *testing.T) {
	c, ctx := svcEnv(t)
	const user = 9

	if _, err := c.DB.Write(ctx,
		`INSERT INTO wallets(user_id,balance,currency) VALUES(?,5000000,'IRR')
		 ON CONFLICT(user_id) DO UPDATE SET balance=5000000`, user); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := c.DB.Write(ctx, `DELETE FROM wallet_transactions WHERE user_id=?`, user); err != nil {
		t.Fatalf("clear ledger: %v", err)
	}
	const start = 5_000_000

	parallel(60, func(i int) {
		if i%2 == 0 {
			_, _ = WalletCredit(ctx, c, user, 100_000, "topup", fmt.Sprintf("MIX-C-%d", i))
			return
		}
		_, _ = WalletDebit(ctx, c, user, 150_000, "purchase", fmt.Sprintf("MIX-D-%d", i))
	})

	balance := WalletBalance(ctx, c, user)
	if balance < 0 {
		t.Errorf("balance went negative: %d", balance)
	}
	ledger, err := c.DB.Count(ctx,
		`SELECT COALESCE(SUM(amount),0) FROM wallet_transactions WHERE user_id=?`, user)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if want := start + ledger; balance != want {
		t.Errorf("balance %d does not match start %d plus ledger %d = %d", balance, start, ledger, want)
	}
}

func TestStressConcurrentReservationsCannotOversell(t *testing.T) {
	c, ctx := svcEnv(t)
	const (
		stock    = 15
		attempts = 60
	)
	variantID, warehouseID := stockFixture(t, c, stock)

	var granted, refused atomic.Int32
	parallel(attempts, func(i int) {
		err := ReserveStock(ctx, c, variantID, 1, fmt.Sprintf("STRESS-ORD-%d", i))
		if err != nil {
			if !errors.Is(err, ErrInsufficientStock) {
				t.Errorf("unexpected error: %v", err)
			}
			refused.Add(1)
			return
		}
		granted.Add(1)
	})

	var quantity, reserved int
	if err := c.DB.Row(ctx,
		`SELECT quantity,reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`,
		[]any{variantID, warehouseID}, &quantity, &reserved); err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	t.Logf("granted=%d refused=%d reserved=%d of %d", granted.Load(), refused.Load(), reserved, quantity)

	if reserved > quantity {
		t.Errorf("reserved %d units of a %d-unit stock — oversold by %d", reserved, quantity, reserved-quantity)
	}
	if got := int(granted.Load()); got > stock {
		t.Errorf("%d reservations succeeded against %d units", got, stock)
	}
	if reserved != int(granted.Load()) {
		t.Errorf("reserved column says %d but %d callers were told they succeeded", reserved, granted.Load())
	}

	if avail := AvailableStock(ctx, c, variantID); avail < 0 {
		t.Errorf("available stock is negative: %d", avail)
	}
}

func TestStressReserveReleaseChurn(t *testing.T) {
	c, ctx := svcEnv(t)
	variantID, warehouseID := stockFixture(t, c, 100)

	parallel(80, func(i int) {
		ref := fmt.Sprintf("CHURN-%d", i)
		switch i % 4 {
		case 0, 1:
			_ = ReserveStock(ctx, c, variantID, 2, ref)
		case 2:
			_ = ReleaseStock(ctx, c, variantID, 1, false, ref)
		default:
			_ = ReleaseStock(ctx, c, variantID, 1, true, ref)
		}
	})

	var quantity, reserved int
	if err := c.DB.Row(ctx,
		`SELECT quantity,reserved FROM inventory WHERE variant_id=? AND warehouse_id=?`,
		[]any{variantID, warehouseID}, &quantity, &reserved); err != nil {
		t.Fatalf("read inventory: %v", err)
	}
	if reserved < 0 {
		t.Errorf("reserved went negative: %d", reserved)
	}
	if quantity < 0 {
		t.Errorf("quantity went negative: %d", quantity)
	}
	if reserved > quantity {
		t.Errorf("reserved %d exceeds quantity %d", reserved, quantity)
	}
}

func TestStressCouponUsageLimitHolds(t *testing.T) {
	c, ctx := svcEnv(t)
	const (
		limit    = 5
		attempts = 40
	)

	if _, err := c.DB.Write(ctx,
		`INSERT INTO coupons(code,type,value,usage_limit,per_user_limit,used_count,active,min_subtotal)
		 VALUES('STRESS50','percent',10,?,0,0,1,0)`, limit); err != nil {
		t.Fatalf("coupon: %v", err)
	}

	var redeemed atomic.Int32
	parallel(attempts, func(i int) {
		if err := RedeemCoupon(ctx, c, "STRESS50", 7, 0, 100_000); err != nil {
			return
		}
		redeemed.Add(1)
	})

	used, err := c.DB.Count(ctx, `SELECT used_count FROM coupons WHERE code='STRESS50'`)
	if err != nil {
		t.Fatalf("read coupon: %v", err)
	}
	t.Logf("redeemed=%d used_count=%d limit=%d", redeemed.Load(), used, limit)

	if used > limit {
		t.Errorf("used_count = %d, exceeds the usage limit of %d", used, limit)
	}
	if got := int(redeemed.Load()); got > limit {
		t.Errorf("%d callers were granted the coupon but the limit is %d", got, limit)
	}
	if used != int(redeemed.Load()) {
		t.Errorf("used_count %d disagrees with the %d successful redemptions", used, redeemed.Load())
	}
}

func TestStressSettingsCacheUnderConcurrentWrites(t *testing.T) {
	c, ctx := svcEnv(t)

	parallel(60, func(i int) {
		switch i % 3 {
		case 0:
			_ = c.SetSetting(ctx, fmt.Sprintf("stress_key_%d", i), fmt.Sprint(i), "private")
		case 1:
			_ = c.AllSettings("public")
		default:
			_ = c.Setting("site_name", "")
			_ = c.SettingBool("maintenance_mode", false)
		}
	})

	for k := range c.AllSettings("public") {
		if len(k) > 11 && k[:11] == "stress_key_" {
			t.Errorf("private setting %q leaked into the public map", k)
		}
	}
}

func TestStressCacheUnderConcurrentAccess(t *testing.T) {
	c, _ := svcEnv(t)

	parallel(80, func(i int) {
		key := fmt.Sprintf("stress:%d", i%8)
		switch i % 4 {
		case 0:
			c.CacheSet(key, i, 0)
		case 1:
			_, _ = c.CacheGet(key)
		case 2:
			_ = c.CacheSize()
		default:
			_ = c.CacheClear("stress:")
		}
	})
	if n := c.CacheSize(); n < 0 {
		t.Errorf("cache size is negative: %d", n)
	}
}
