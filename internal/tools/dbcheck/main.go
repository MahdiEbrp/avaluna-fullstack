package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/arena/avaluna/internal/database"
)

const defaultQuery = "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name"

func main() {
	path := os.Getenv("DATABASE_PATH")
	if path == "" {
		path = "data/store.db"
	}
	if err := run(os.Args[1:], path, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, path string, out io.Writer) error {
	q := strings.TrimSpace(strings.Join(args, " "))
	if q == "" {
		q = defaultQuery
	}

	db, err := database.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Maps(context.Background(), q)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintln(out, r); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintln(out, "rows:", len(rows))
	return err
}
