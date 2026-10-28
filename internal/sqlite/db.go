// Package sqlite owns database initialization and versioned schema migrations.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// Open opens a local file, configures each connection and probes FTS5.
// The caller must run Migrate before exposing the HTTP listener.
func Open(ctx context.Context, path string) (_ *sql.DB, err error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open SQLite: %w", err)
	}
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("open SQLite: database file path must not be empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, fmt.Errorf("create SQLite directory: %w", err)
	}
	// Encode filenames so '?' and '#' cannot become driver options.
	uriPath := filepath.ToSlash(abs)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	u := url.URL{Scheme: "file", Path: uriPath}
	q := url.Values{}
	for _, pragma := range []string{"foreign_keys(1)", "busy_timeout(2000)", "synchronous(FULL)", "cache_size(-2048)", "wal_autocheckpoint(1000)"} {
		q.Add("_pragma", pragma)
	}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite driver: %w", err)
	}
	defer func() {
		if err != nil {
			db.Close()
		}
	}()
	// M1 has no workload requiring multiple pools; serialize through one connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping SQLite during initialization: %w", err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return nil, fmt.Errorf("enable SQLite WAL: %w", err)
	}
	if mode != "wal" {
		return nil, fmt.Errorf("enable SQLite WAL: got journal mode %q", mode)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire SQLite FTS5 probe connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE VIRTUAL TABLE temp.akh_fts5_probe USING fts5(content)"); err != nil {
		return nil, fmt.Errorf("verify SQLite FTS5 support: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "DROP TABLE temp.akh_fts5_probe"); err != nil {
		return nil, fmt.Errorf("clean SQLite FTS5 probe: %w", err)
	}
	return db, nil
}
