package httpapi

import (
	"context"
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

func TestKnowledgeAPIRequiresBearerToken(t *testing.T) {
	h, logs := apiFixture(t)
	for _, tc := range []struct {
		name, header string
		want         int
	}{
		{"missing", "", 401},
		{"wrong", "Bearer not-the-token", 401},
		{"valid", "Bearer " + testToken, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want || (tc.want == 401 && !strings.Contains(w.Body.String(), `"unauthorized"`)) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	health := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	health.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, health)
	if w.Code != 200 {
		t.Fatalf("healthz status=%d", w.Code)
	}
	if strings.Contains(logs.String(), testToken) || strings.Contains(logs.String(), "Authorization") {
		t.Fatalf("credential leaked to log: %s", logs.String())
	}
}

func TestRevokedAndExpiredTokensAreRejected(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := sqlite.NewTokenStore(db)
	now := time.Now().Add(-2 * time.Hour).UTC()
	expired, err := store.Issue(ctx, "expired", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := store.Issue(ctx, "revoked", 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Revoke(ctx, revoked.ID, now); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), store, slog.New(slog.NewJSONHandler(&logs, nil)))
	for _, secret := range []string{expired.Token, revoked.Token} {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 || strings.Contains(w.Body.String(), secret) || strings.Contains(logs.String(), secret) {
			t.Fatalf("status=%d body=%s log=%s", w.Code, w.Body.String(), logs.String())
		}
	}
}
