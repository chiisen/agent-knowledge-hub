package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenInitializesSQLite(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "folder ?#", "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for query, want := range map[string]string{
		"PRAGMA foreign_keys": "1", "PRAGMA journal_mode": "wal", "PRAGMA busy_timeout": "2000",
	} {
		var got string
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", query, got, want)
		}
	}
	// Replacing the physical connection must not lose connection-scoped PRAGMAs.
	db.SetMaxIdleConns(0)
	for query, want := range map[string]int{"PRAGMA foreign_keys": 1, "PRAGMA busy_timeout": 2000} {
		var got int
		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("new connection %s = %d, err = %v", query, got, err)
		}
	}
	var version string
	if err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("embedded SQLite: %s", version)
}

func TestOpenSupportsFTS5(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "CREATE VIRTUAL TABLE temp.fts_probe USING fts5(content)"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO temp.fts_probe(content) VALUES (?)", "SQLite initialization"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM temp.fts_probe WHERE fts_probe MATCH ?", "SQLite").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("FTS5 matched %d rows", count)
	}
}

func TestOpenRejectsInvalidPathAndCanceledContext(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("empty path accepted")
	}
	if _, err := Open(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "SQLite") {
		t.Fatalf("directory open error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open error = %v", err)
	}
}
