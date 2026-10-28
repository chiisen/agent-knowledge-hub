package sqlite

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
)

func TestUpgradeFromMilestone1PreservesData(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	script, err := fs.ReadFile(migrationFiles, "migrations/001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(ctx, db, fstest.MapFS{"001_initial.sql": &fstest.MapFile{Data: script}}); err != nil {
		t.Fatal(err)
	}
	id := "5f6c5190-d845-4d45-92f6-43e3d8425f8f"
	if _, err := db.ExecContext(ctx, `INSERT INTO knowledge(id,title,content,type,scope,source,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, id, "legacy", "retained", "type", "scope", "source", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	legacySearch, err := NewRepository(db).Search(ctx, `"retained"`, knowledge.Filter{Status: "draft", Limit: 20})
	if err != nil || len(legacySearch.Items) != 1 || legacySearch.Items[0].ID != id {
		t.Fatalf("legacy FTS backfill = %+v, err=%v", legacySearch, err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	got, err := NewRepository(db).Get(ctx, id)
	if err != nil || got.Content != "retained" || got.Version != 1 || got.ArchivedAt != nil {
		t.Fatalf("upgraded = %+v, err=%v", got, err)
	}
}

func TestRepositoryCRUDConflictArchiveAndCancellation(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := NewRepository(db)
	svc := knowledge.NewService(repo)
	k, err := svc.Create(ctx, knowledge.Create{Title: "title", Content: "secret ' SQL", Type: "type", Scope: "project", Source: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, content := range []string{"one", "two"} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			_, err := svc.Update(ctx, k.ID, k.Version, knowledge.Patch{Content: &s})
			outcomes <- err
		}(content)
	}
	wg.Wait()
	close(outcomes)
	success, conflicts := 0, 0
	for err := range outcomes {
		if err == nil {
			success++
		} else if errors.Is(err, knowledge.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", success, conflicts)
	}
	current, err := svc.Get(ctx, k.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Archive(ctx, k.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	preserved, err := repo.Get(ctx, k.ID)
	if err != nil || preserved.ArchivedAt == nil || preserved.Content != current.Content || preserved.Version != current.Version+1 {
		t.Fatalf("archive did not preserve data: %+v, err=%v", preserved, err)
	}
	if err := svc.Archive(ctx, k.ID, current.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, k.ID); !errors.Is(err, knowledge.ErrNotFound) {
		t.Fatalf("archived GET = %v", err)
	}
	list, err := svc.List(ctx, knowledge.Filter{Status: "draft", Limit: 20})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("archived list = %+v, err = %v", list, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repo.Get(canceled, k.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled GET = %v", err)
	}
	if _, err := repo.List(canceled, knowledge.Filter{Status: "draft", Limit: 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list = %v", err)
	}
	if _, err := svc.Search(canceled, "secret", knowledge.Filter{Status: "draft", Limit: 20}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search = %v", err)
	}
	if _, err := svc.Create(canceled, knowledge.Create{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled create = %v", err)
	}
	if _, err := repo.Create(canceled, k); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SQL create = %v", err)
	}
	if _, err := repo.Update(canceled, current, current.Version); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SQL update = %v", err)
	}
	if err := repo.Archive(canceled, current.ID, current.Version, current.UpdatedAt); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled SQL archive = %v", err)
	}
}

func TestSearchIndexTracksCreateUpdateAndArchive(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	svc := knowledge.NewService(NewRepository(db))
	k, err := svc.Create(ctx, knowledge.Create{Title: "SQLite trigram", Content: "支付模組採用短交易鎖", Type: "decision", Scope: "project", Project: ptr("alpha"), Status: "confirmed", Source: "review note"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := svc.Search(ctx, "支付模組", knowledge.Filter{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != k.ID || page.Items[0].Source != "review note" {
		t.Fatalf("Chinese search = %+v, %v", page, err)
	}
	old := k.Content
	updated, err := svc.Update(ctx, k.ID, k.Version, knowledge.Patch{Content: ptr("SQLite query uses bound parameters")})
	if err != nil {
		t.Fatal(err)
	}
	page, err = svc.Search(ctx, "支付模組", knowledge.Filter{Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("stale content remains searchable: %+v, %v", page, err)
	}
	page, err = svc.Search(ctx, "bound parameters", knowledge.Filter{Status: "draft", Limit: 20})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("updated content not indexed: %+v, %v", page, err)
	}
	if err := svc.Archive(ctx, k.ID, updated.Version); err != nil {
		t.Fatal(err)
	}
	page, err = svc.Search(ctx, "bound parameters", knowledge.Filter{Status: "draft", Limit: 20})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("archived item searchable: %+v, %v", page, err)
	}
	if old == updated.Content {
		t.Fatal("test fixture did not change content")
	}
}

func TestSearchRanksTitleMatchesAboveContentMatches(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	svc := knowledge.NewService(NewRepository(db))
	for _, input := range []knowledge.Create{
		{Title: "prioritymarker", Content: "The title has the match.", Type: "decision", Scope: "project", Status: "confirmed", Source: "test"},
		{Title: "Unrelated heading", Content: "The content has prioritymarker.", Type: "decision", Scope: "project", Status: "confirmed", Source: "test"},
	} {
		if _, err := svc.Create(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.Search(ctx, "prioritymarker", knowledge.Filter{Limit: 20})
	if err != nil || len(page.Items) != 2 || page.Items[0].Title != "prioritymarker" {
		t.Fatalf("BM25 ranking = %+v, err=%v", page, err)
	}
}

func ptr(s string) *string { return &s }
