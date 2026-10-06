package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arena/avaluna/internal/database"
)

func freshDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dbcheck.db")
	db, err := database.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return path
}

func TestNoArgumentsListsTheTables(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, freshDB(t), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()

	for _, table := range []string{"users", "products", "orders", "settings"} {
		if !strings.Contains(got, table) {
			t.Errorf("table %q missing from the default listing", table)
		}
	}
	if !strings.Contains(got, "rows:") {
		t.Error("no row count was printed")
	}
}

func TestExplicitQueryIsJoinedFromArgs(t *testing.T) {
	var out bytes.Buffer

	args := []string{"SELECT", "COUNT(*)", "AS", "n", "FROM", "settings"}
	if err := run(args, freshDB(t), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "n:") {
		t.Errorf("output %q does not contain the aliased column", out.String())
	}
	if !strings.Contains(out.String(), "rows: 1") {
		t.Errorf("output %q, want exactly one row", out.String())
	}
}

func TestEmptyResultStillReportsACount(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"SELECT id FROM users WHERE email='nobody@nowhere'"}, freshDB(t), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "rows: 0" {
		t.Errorf("output = %q, want a zero count", got)
	}
}

func TestBlankArgumentsFallBackToTheDefault(t *testing.T) {
	var out bytes.Buffer

	if err := run([]string{"  ", "\t"}, freshDB(t), &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "users") {
		t.Errorf("blank input did not fall back to the table listing: %q", out.String())
	}
}

func TestInvalidSQLReturnsAnError(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"SELECT * FROM table_that_does_not_exist"}, freshDB(t), &out)
	if err == nil {
		t.Fatal("a query against a missing table succeeded")
	}
	if !strings.Contains(err.Error(), "table_that_does_not_exist") {
		t.Errorf("error = %v, want it to name the problem", err)
	}
}

func TestUnopenableDatabaseReportsThePath(t *testing.T) {
	var out bytes.Buffer

	bad := t.TempDir()
	err := run(nil, bad, &out)
	if err == nil {
		t.Fatal("opening a directory as a database succeeded")
	}
	if !strings.Contains(err.Error(), bad) {
		t.Errorf("error = %v, want it to name %q", err, bad)
	}
	if out.Len() != 0 {
		t.Errorf("a failed open still wrote output: %q", out.String())
	}
}

func TestDatabaseIsCreatedWhenAbsent(t *testing.T) {
	var out bytes.Buffer

	fresh := filepath.Join(t.TempDir(), "nested", "new.db")
	if err := run(nil, fresh, &out); err != nil {
		t.Fatalf("run against a new database: %v", err)
	}
	if !strings.Contains(out.String(), "rows:") {
		t.Errorf("output = %q, want a row count", out.String())
	}
}
