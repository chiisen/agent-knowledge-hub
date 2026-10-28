package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestKnowledgeMigrationPreservesDataAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "knowledge.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	id := "d1147481-c608-44cc-9703-b2307353d866"
	content := "SQLite 經驗 'quoted'; DROP TABLE knowledge;"
	if _, err := db.ExecContext(ctx, `INSERT INTO knowledge
		(id, title, content, type, scope, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, "經驗", content, "custom-type", "custom-scope", "人工筆記，非 JSON", 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var got, status string
	var project, module *string
	var created, updated int64
	if err := db.QueryRowContext(ctx, `SELECT content, status, project, module, created_at, updated_at
		FROM knowledge WHERE id = ?`, id).Scan(&got, &status, &project, &module, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if got != content || status != "draft" || project != nil || module != nil || created != 1000 || updated != 1000 {
		t.Fatalf("data changed: content=%q status=%q project=%v module=%v times=%d/%d", got, status, project, module, created, updated)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("migration history count = %d", count)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO knowledge (id, title, content, type, scope, status, source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, "duplicate", "content", "type", "scope", "draft", "source", 1, 1); err == nil {
		t.Fatal("duplicate UUID accepted")
	}
}

func TestKnowledgeSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "confirmed", "deprecated", "invalid"} {
		t.Run(status, func(t *testing.T) {
			_, err := db.ExecContext(ctx, `INSERT INTO knowledge (id, title, content, type, scope, status, source, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, status, "title", "content", "type", "scope", status, "source", 1, 1)
			if (err != nil) != (status == "invalid") {
				t.Fatalf("status %q: %v", status, err)
			}
		})
	}
	for _, tc := range []struct {
		name            string
		id, kind, scope any
	}{
		{"null-id", nil, "type", "scope"}, {"empty-type", "id1", "", "scope"},
		{"blank-scope", "id2", "type", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, `INSERT INTO knowledge (id, title, content, type, scope, source, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, tc.id, "title", "content", tc.kind, tc.scope, "source", 1, 1)
			if err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}

func TestMigrateHonorsCanceledContext(t *testing.T) {
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Migrate(ctx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled migration error = %v", err)
	}
}

func TestMigrationsAreAtomicAndDetectChangedHistory(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	migrations := fstest.MapFS{
		"001_first.sql":  &fstest.MapFile{Data: []byte("CREATE TABLE first (id INTEGER PRIMARY KEY);")},
		"002_second.sql": &fstest.MapFile{Data: []byte("CREATE TABLE second (id INTEGER); INVALID SQL;")},
	}
	if err := runMigrations(ctx, db, migrations); err == nil || !strings.Contains(err.Error(), "002_second.sql") {
		t.Fatalf("migration error = %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE name = ?", "first").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration left partial schema")
	}
	delete(migrations, "002_second.sql")
	if err := runMigrations(ctx, db, migrations); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, migrations); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	migrations["001_first.sql"].Data = []byte("CREATE TABLE different (id INTEGER);")
	if err := runMigrations(ctx, db, migrations); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed history error = %v", err)
	}
}

func TestMigrationsRejectUnknownAndGappedVersions(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := fstest.MapFS{"001_first.sql": &fstest.MapFile{Data: []byte("CREATE TABLE first (id INTEGER);")}}
	if err := runMigrations(ctx, db, first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)", 2, "002_future.sql", "unknown", 0); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, first); err == nil {
		t.Fatal("unknown schema version accepted")
	}
	if err := runMigrations(ctx, db, fstest.MapFS{"002_gap.sql": &fstest.MapFile{Data: []byte("SELECT 1;")}}); err == nil {
		t.Fatal("gapped migration files accepted")
	}
}
