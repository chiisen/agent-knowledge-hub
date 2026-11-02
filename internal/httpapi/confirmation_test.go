package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
)

func TestConfirmationRecordsTokenName(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "confirm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewTokenStore(db)
	issued, err := store.Issue(ctx, "reviewer", []string{"alpha"}, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	callAs(t, h, issued.Token, http.MethodPost, collection, `{"title":"title","content":"content","type":"decision","scope":"project","project":"alpha","status":"confirmed","source":"source","confirmed_by":"client"}`, "", 422)
	created := record(t, callAs(t, h, issued.Token, http.MethodPost, collection, `{"title":"title","content":"content","type":"decision","scope":"project","project":"alpha","source":"source"}`, "", 201))
	if created.ConfirmedBy != nil || created.ConfirmedAt != nil || created.Status != "draft" {
		t.Fatalf("draft stored a confirmer: %+v", created)
	}
	confirmed := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"confirmed"}`, `"1"`, 200))
	if confirmed.ConfirmedBy == nil || *confirmed.ConfirmedBy != "reviewer" || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirmer = %+v", confirmed)
	}
	repeated := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"confirmed"}`, `"2"`, 200))
	if repeated.ConfirmedBy == nil || *repeated.ConfirmedBy != "reviewer" || !repeated.ConfirmedAt.Equal(*confirmed.ConfirmedAt) {
		t.Fatalf("repeat confirmation changed confirmer: %+v", repeated)
	}
	deprecated := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"deprecated"}`, `"3"`, 200))
	if deprecated.ConfirmedBy == nil || *deprecated.ConfirmedBy != "reviewer" {
		t.Fatalf("deprecated dropped confirmer: %+v", deprecated)
	}
	time.Sleep(2 * time.Millisecond)
	restored := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"confirmed"}`, `"4"`, 200))
	if restored.ConfirmedAt == nil || !restored.ConfirmedAt.After(*confirmed.ConfirmedAt) {
		t.Fatalf("reconfirmation did not record a new time: %+v", restored)
	}
	demoted := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"content":"changed content"}`, `"5"`, 200))
	if demoted.Status != "draft" || demoted.ConfirmedBy != nil || demoted.ConfirmedAt != nil {
		t.Fatalf("demotion kept confirmer: %+v", demoted)
	}
	if _, err := db.ExecContext(ctx, `UPDATE api_tokens SET name = ? WHERE id = ?`, "renamed", issued.ID); err != nil {
		t.Fatal(err)
	}
	renamed := record(t, callAs(t, h, issued.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"confirmed"}`, `"6"`, 200))
	if renamed.ConfirmedBy == nil || *renamed.ConfirmedBy != "renamed" {
		t.Fatalf("new confirmation = %+v", renamed)
	}
	page := callAs(t, h, issued.Token, http.MethodGet, collection, "", "", 200)
	if !strings.Contains(page.Body.String(), `"confirmed_by":"renamed"`) {
		t.Fatalf("list omitted confirmer: %s", page.Body.String())
	}
	search := callAs(t, h, issued.Token, http.MethodPost, collection+"/search", `{"query":"changed"}`, "", 200)
	if strings.Contains(search.Body.String(), "confirmed_by") || strings.Contains(search.Body.String(), "confirmed_at") {
		t.Fatalf("search returned confirmation fields: %s", search.Body.String())
	}
}
