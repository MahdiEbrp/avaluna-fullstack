package database

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type DB struct {
	*sql.DB
	wmu  sync.Mutex
	path string
}

func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-64000)"
	sdb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sdb.SetMaxOpenConns(1)
	sdb.SetMaxIdleConns(1)
	sdb.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sdb.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &DB{DB: sdb, path: path}, nil
}

func (d *DB) Path() string { return d.path }

func (d *DB) Migrate(ctx context.Context) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	if _, err := d.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *DB) Write(ctx context.Context, query string, args ...any) (sql.Result, error) {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return d.ExecContext(ctx, query, args...)
}

func (d *DB) Row(ctx context.Context, query string, args []any, dest ...any) error {
	err := d.QueryRowContext(ctx, query, args...).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

var ErrNotFound = errors.New("record not found")

func (d *DB) Count(ctx context.Context, query string, args ...any) (int, error) {
	var n int
	err := d.QueryRowContext(ctx, query, args...).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (d *DB) Scalar(ctx context.Context, query string, args ...any) (sql.NullString, error) {
	var v sql.NullString
	err := d.QueryRowContext(ctx, query, args...).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	return v, err
}

func (d *DB) Maps(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, 16)
	for rows.Next() {
		raw := make([]any, len(cols))
		ptr := make([]any, len(cols))
		for i := range raw {
			ptr[i] = &raw[i]
		}
		if err := rows.Scan(ptr...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalize(raw[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (d *DB) MapOne(ctx context.Context, query string, args ...any) (map[string]any, error) {
	rows, err := d.Maps(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return rows[0], nil
}

func normalize(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	default:
		return v
	}
}

func (d *DB) Healthy(ctx context.Context) error {
	var one int
	return d.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

func (d *DB) Backup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return err
	}
	_ = os.Remove(dest)
	d.wmu.Lock()
	defer d.wmu.Unlock()
	_, err := d.ExecContext(ctx, "VACUUM INTO "+quote(dest))
	return err
}

func (d *DB) Stats(ctx context.Context) (map[string]int, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	tables := []string{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tables = append(tables, t)
	}
	out := map[string]int{}
	for _, t := range tables {
		if strings.HasSuffix(t, "_data") || strings.HasSuffix(t, "_idx") ||
			strings.HasSuffix(t, "_content") || strings.HasSuffix(t, "_docsize") ||
			strings.HasSuffix(t, "_config") {
			continue
		}
		n, err := d.Count(ctx, "SELECT COUNT(*) FROM "+t)
		if err != nil {
			continue
		}
		out[t] = n
	}
	return out, nil
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
