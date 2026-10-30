package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
)

func TestProjectWhitelistIsolatesKnowledge(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "authz.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewTokenStore(db)
	now := time.Now()
	alpha, err := store.Issue(ctx, "alpha", []string{"alpha"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	beta, err := store.Issue(ctx, "beta", []string{"beta"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	created := record(t, callAs(t, h, alpha.Token, http.MethodPost, collection, `{"title":"title","content":"alpha-content","type":"decision","scope":"personal","project":"alpha","source":"source"}`, "", 201))
	betaItem := record(t, callAs(t, h, beta.Token, http.MethodPost, collection, `{"title":"title","content":"beta-content","type":"decision","scope":"project","project":"beta","status":"confirmed","source":"source"}`, "", 201))
	callAs(t, h, alpha.Token, http.MethodPost, collection, `{"title":"title","content":"content","type":"decision","scope":"personal","source":"source"}`, "", 403)
	callAs(t, h, alpha.Token, http.MethodPost, collection, `{"title":"title","content":"content","type":"decision","scope":"project","project":"beta","source":"source"}`, "", 403)
	callAs(t, h, alpha.Token, http.MethodGet, collection+"/"+betaItem.ID, "", "", 404)
	callAs(t, h, alpha.Token, http.MethodPatch, collection+"/"+betaItem.ID, `{"title":"stolen"}`, `"1"`, 404)
	callAs(t, h, alpha.Token, http.MethodDelete, collection+"/"+betaItem.ID, "", `"1"`, 404)
	callAs(t, h, alpha.Token, http.MethodPatch, collection+"/"+created.ID, `{"project":"beta"}`, `"1"`, 403)
	kept := record(t, callAs(t, h, alpha.Token, http.MethodGet, collection+"/"+created.ID, "", "", 200))
	if kept.Project == nil || *kept.Project != "alpha" || kept.Version != 1 {
		t.Fatalf("project changed after forbidden patch: %+v", kept)
	}
	page := recordPage(t, callAs(t, h, alpha.Token, http.MethodGet, collection+"?status=confirmed", "", "", 200))
	if len(page) != 0 {
		t.Fatalf("alpha token saw confirmed rows: %+v", page)
	}
	confirmed := record(t, callAs(t, h, alpha.Token, http.MethodPatch, collection+"/"+created.ID, `{"status":"confirmed"}`, `"1"`, 200))
	page = recordPage(t, callAs(t, h, alpha.Token, http.MethodGet, collection, "", "", 200))
	if len(page) != 1 || page[0].ID != confirmed.ID {
		t.Fatalf("alpha list = %+v", page)
	}
	search := callAs(t, h, alpha.Token, http.MethodPost, collection+"/search", `{"query":"content"}`, "", 200)
	if strings.Contains(search.Body.String(), betaItem.ID) || strings.Contains(search.Body.String(), "beta-content") {
		t.Fatalf("search leaked beta knowledge: %s", search.Body.String())
	}
	if !strings.Contains(search.Body.String(), confirmed.ID) {
		t.Fatalf("search missed alpha knowledge: %s", search.Body.String())
	}
}

func callAs(t *testing.T, h http.Handler, secret, method, path, body, etag string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+secret)
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	return w
}
