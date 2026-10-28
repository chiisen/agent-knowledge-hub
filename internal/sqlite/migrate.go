package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate validates history and applies pending, compiled-in schema migrations.
func Migrate(ctx context.Context, db *sql.DB) error {
	files, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded SQLite migrations: %w", err)
	}
	return runMigrations(ctx, db, files)
}

type migration struct {
	version  int
	name     string
	checksum string
	script   string
}

// runMigrations applies the embedded, trusted SQL and its history atomically.
// Migration files are immutable after release; gaps and unknown history fail closed.
func runMigrations(ctx context.Context, db *sql.DB, files fs.FS) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return fmt.Errorf("read SQLite migrations: %w", err)
	}
	var migrations []migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, err := strconv.Atoi(prefix)
		if !ok || len(prefix) != 3 || err != nil || version != len(migrations)+1 {
			return fmt.Errorf("validate SQLite migration %q: versions must be contiguous from 001", entry.Name())
		}
		body, err := fs.ReadFile(files, entry.Name())
		if err != nil {
			return fmt.Errorf("read SQLite migration %q: %w", entry.Name(), err)
		}
		migrations = append(migrations, migration{version, entry.Name(), fmt.Sprintf("%x", sha256.Sum256(body)), string(body)})
	}
	if len(migrations) == 0 {
		return fmt.Errorf("validate SQLite migrations: no SQL files found")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migrations: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		checksum TEXT NOT NULL,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create SQLite migration history: %w", err)
	}
	rows, err := tx.QueryContext(ctx, "SELECT version, name, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("read SQLite migration history: %w", err)
	}
	applied := 0
	for rows.Next() {
		var version int
		var name, checksum string
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("scan SQLite migration history: %w", err)
		}
		if applied >= len(migrations) || version != applied+1 {
			rows.Close()
			return fmt.Errorf("validate SQLite migration history: unknown or noncontiguous version %d", version)
		}
		expected := migrations[applied]
		if name != expected.name || checksum != expected.checksum {
			rows.Close()
			return fmt.Errorf("validate SQLite migration %d: name or checksum mismatch", version)
		}
		applied++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate SQLite migration history: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close SQLite migration history: %w", err)
	}
	for _, m := range migrations[applied:] {
		// Only compiled-in SQL is executed; request data never enters migration scripts.
		if _, err := tx.ExecContext(ctx, m.script); err != nil {
			return fmt.Errorf("apply SQLite migration %q: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)", m.version, m.name, m.checksum, time.Now().UTC().UnixMilli()); err != nil {
			return fmt.Errorf("record SQLite migration %q: %w", m.name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migrations: %w", err)
	}
	return nil
}
