package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
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

func TestOpenSupportsConcurrentConnectionsWithConnectionPragmas(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Stats().MaxOpenConnections < 4 {
		t.Fatalf("MaxOpenConnections=%d, want at least 4", db.Stats().MaxOpenConnections)
	}
	const n = 4
	conns := make([]*sql.Conn, n)
	for i := range conns {
		conns[i], err = db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for _, conn := range conns {
		wg.Add(1)
		go func(conn *sql.Conn) {
			defer wg.Done()
			for query, want := range map[string]int{
				"PRAGMA foreign_keys":       1,
				"PRAGMA busy_timeout":       2000,
				"PRAGMA synchronous":        2,
				"PRAGMA cache_size":         -2048,
				"PRAGMA wal_autocheckpoint": 1000,
			} {
				var got int
				if err := conn.QueryRowContext(ctx, query).Scan(&got); err != nil {
					errCh <- err
					return
				}
				if got != want {
					errCh <- fmt.Errorf("%s=%d, want %d", query, got, want)
					return
				}
			}
		}(conn)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

func TestCheckpointTruncatesWALAndIntegrityCheck(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "checkpoint.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO knowledge(id,title,content,type,scope,status,source,created_at,updated_at) VALUES('id','title','content','type','scope','draft','source',1,1)"); err != nil {
		t.Fatal(err)
	}
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity_check=%q err=%v", integrity, err)
	}
	if err := Checkpoint(ctx, db); err != nil {
		t.Fatal(err)
	}
	var busy int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, new(int), new(int)); err != nil || busy != 0 {
		t.Fatalf("checkpoint busy=%d err=%v", busy, err)
	}
}

func TestStoppedDatabaseBackupCanBeRestored(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	db, err := Open(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO knowledge(id,title,content,type,scope,status,source,created_at,updated_at) VALUES('backup-id','backup title','backup content','decision','project','confirmed','test',1,1)"); err != nil {
		t.Fatal(err)
	}
	if err := Checkpoint(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "backup.db")
	bytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backup, bytes, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, backup)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := Migrate(ctx, restored); err != nil {
		t.Fatal(err)
	}
	var title string
	if err := restored.QueryRowContext(ctx, "SELECT title FROM knowledge WHERE id=?", "backup-id").Scan(&title); err != nil || title != "backup title" {
		t.Fatalf("restored title=%q err=%v", title, err)
	}
}

func BenchmarkSQLiteCreate(b *testing.B) {
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "benchmark.db")
	db, err := Open(ctx, dbPath)
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		b.Fatal(err)
	}
	repo := NewRepository(db)
	service := knowledge.NewService(repo)
	b.ReportAllocs()
	b.Logf("environment=%s/%s %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := service.Create(ctx, knowledge.Create{Title: "benchmark title", Content: "benchmark content", Type: "decision", Scope: "project", Source: "benchmark"}); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if err := Checkpoint(ctx, db); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(info.Size()), "db-B")
	if walInfo, err := os.Stat(dbPath + "-wal"); err == nil {
		b.ReportMetric(float64(walInfo.Size()), "wal-B")
	}
	b.ReportMetric(float64(b.N), "rows")
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
