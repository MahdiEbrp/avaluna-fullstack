package database_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/config"
	"github.com/arena/avaluna/internal/database"
)

func fresh(t *testing.T) (*database.DB, *config.Config) {
	t.Helper()
	cfg := config.Load()
	cfg.DatabasePath = filepath.Join(t.TempDir(), "unit.db")
	db, err := database.Open(cfg.DatabasePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db, cfg
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, _ := fresh(t)
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("second migration failed: %v", err)
	}
	n, err := db.Count(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n < 60 {
		t.Errorf("only %d tables created, the schema covers 31 modules", n)
	}
}

func TestWALAndForeignKeysEnabled(t *testing.T) {
	db, _ := fresh(t)
	ctx := context.Background()
	mode, err := db.Scalar(ctx, `PRAGMA journal_mode`)
	if err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if !strings.EqualFold(mode.String, "wal") {
		t.Errorf("journal_mode = %q, want wal", mode.String)
	}
	fk, err := db.Count(ctx, `PRAGMA foreign_keys`)
	if err != nil {
		t.Fatalf("pragma fk: %v", err)
	}
	if fk != 1 {
		t.Error("foreign keys must be enforced")
	}
}

func TestSeedPopulatesEveryDomain(t *testing.T) {
	db, cfg := fresh(t)
	ctx := context.Background()
	if db.Seeded(ctx) {
		t.Fatal("a fresh database must not report itself as seeded")
	}
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if !db.Seeded(ctx) {
		t.Error("Seeded() should be true after seeding")
	}

	minimums := map[string]int{
		"users": 5, "categories": 5, "brands": 4, "products": 24, "product_variants": 24,
		"inventory": 24, "orders": 10, "order_items": 10, "coupons": 3, "blog_posts": 5,
		"reviews": 5, "settings": 15, "cron_jobs": 24, "feature_flags": 3,
		"shipping_methods": 2, "payment_methods": 2, "warehouses": 1, "vendors": 2,
		"pages": 3, "faqs": 3, "currencies": 2, "tax_rules": 1,
	}
	for table, min := range minimums {
		n, err := db.Count(ctx, "SELECT COUNT(*) FROM "+table)
		if err != nil {
			t.Errorf("count %s: %v", table, err)
			continue
		}
		if n < min {
			t.Errorf("%s has %d rows, want at least %d", table, n, min)
		}
	}
}

func TestSeedIsIdempotent(t *testing.T) {
	db, cfg := fresh(t)
	ctx := context.Background()
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	before, _ := db.Count(ctx, `SELECT COUNT(*) FROM products`)
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	after, _ := db.Count(ctx, `SELECT COUNT(*) FROM products`)
	if before != after {
		t.Errorf("re-seeding duplicated rows: %d -> %d", before, after)
	}
}

func TestTransactionRollsBackOnError(t *testing.T) {
	db, cfg := fresh(t)
	ctx := context.Background()
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before, _ := db.Count(ctx, `SELECT COUNT(*) FROM categories`)

	err := db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO categories(slug,name,name_en) VALUES('tx-test','تست','TX Test')`); err != nil {
			return err
		}
		return errTest
	})
	if err == nil {
		t.Fatal("Tx should surface the callback error")
	}
	after, _ := db.Count(ctx, `SELECT COUNT(*) FROM categories`)
	if before != after {
		t.Errorf("rollback failed: %d -> %d", before, after)
	}
}

type testError struct{}

func (testError) Error() string { return "deliberate failure" }

var errTest = testError{}

func TestBackupProducesAReadableSnapshot(t *testing.T) {
	db, cfg := fresh(t)
	ctx := context.Background()
	if err := database.Seed(ctx, db, cfg); err != nil {
		t.Fatalf("seed: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "snapshot.db")
	if err := db.Backup(ctx, dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	copyDB, err := database.Open(dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = copyDB.Close() }()
	n, err := copyDB.Count(ctx, `SELECT COUNT(*) FROM products`)
	if err != nil || n == 0 {
		t.Errorf("snapshot is unusable: %d rows, err %v", n, err)
	}
}

func TestNotFoundSentinel(t *testing.T) {
	db, _ := fresh(t)
	var id int
	err := db.Row(context.Background(), `SELECT id FROM users WHERE id=-1`, nil, &id)
	if err != database.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}
