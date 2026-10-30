package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
	_ "modernc.org/sqlite"
)

type panicRepository struct{}

func (panicRepository) Create(context.Context, knowledge.Knowledge) (knowledge.Knowledge, error) {
	panic("private panic detail")
}
func (panicRepository) Get(context.Context, string) (knowledge.Knowledge, error) {
	panic("private panic detail")
}
func (panicRepository) List(context.Context, knowledge.Filter) (knowledge.Page, error) {
	panic("private panic detail")
}
func (panicRepository) Search(context.Context, string, knowledge.Filter) (knowledge.SearchPage, error) {
	panic("private panic detail")
}
func (panicRepository) Update(context.Context, knowledge.Knowledge, int64) (knowledge.Knowledge, error) {
	panic("private panic detail")
}
func (panicRepository) Archive(context.Context, string, int64, time.Time) error {
	panic("private panic detail")
}

func TestKnowledgeHandlerRecoversPanicWithoutLeakingDetails(t *testing.T) {
	var logs bytes.Buffer
	h := NewKnowledgeHandler(nil, knowledge.NewService(panicRepository{}), allowAuthenticator{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("panic response = %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String()+logs.String(), "private panic detail") {
		t.Fatal("panic detail leaked")
	}
}

func TestResponseBufferEnforcesEncodedResponseLimit(t *testing.T) {
	buffer := newResponseBuffer()
	buffer.Header().Set("X-Request-ID", "request-1")
	buffer.WriteHeader(http.StatusOK)
	if _, err := buffer.Write(bytes.Repeat([]byte("x"), maxResponseBytes+1)); err == nil {
		t.Fatal("oversized write accepted")
	}
	w := httptest.NewRecorder()
	buffer.commit(w, "request-1")
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "response_too_large") || !strings.Contains(w.Body.String(), "request-1") {
		t.Fatalf("oversized response = %d %s", w.Code, w.Body.String())
	}
}

type oversizedRepository struct{ *sqlite.Repository }

func (oversizedRepository) List(context.Context, knowledge.Filter) (knowledge.Page, error) {
	items := make([]knowledge.Knowledge, 17)
	for i := range items {
		items[i] = knowledge.Knowledge{ID: "oversized", Title: "title", Content: strings.Repeat("x", 64<<10), Type: "decision", Scope: "project", Status: "confirmed", Source: "test", Version: 1, UsableAsRule: true}
	}
	return knowledge.Page{Items: items}, nil
}

func TestKnowledgeAPIRejectsOversizedJSONResponse(t *testing.T) {
	service := knowledge.NewService(&oversizedRepository{sqlite.NewRepository(nil)})
	h := NewKnowledgeHandler(nil, service, allowAuthenticator{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/knowledge", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "response_too_large") {
		t.Fatalf("oversized API response = %d %s", w.Code, w.Body.String())
	}
}

type allowAuthenticator struct{}

func (allowAuthenticator) Authenticate(context.Context, string, time.Time) error { return nil }

func BenchmarkHealthHandler(b *testing.B) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	h := NewHandler(db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	}
}

func TestHealthAndErrors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var logs bytes.Buffer
	h := NewHandler(db, slog.New(slog.NewJSONHandler(&logs, nil)))
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/healthz", 200}, {"POST", "/healthz", 405}, {"GET", "/missing", 404},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path+"?secret=hidden-query", strings.NewReader("hidden-body"))
			r.Header.Set("Authorization", "Bearer hidden-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/json" {
				t.Fatal("response is not JSON")
			}
			if w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.want == 200 && body["status"] != "ok" {
				t.Fatalf("body = %v", body)
			}
			if tc.want != 200 {
				detail, ok := body["error"].(map[string]any)
				if !ok || detail["code"] == "" || detail["request_id"] != w.Header().Get("X-Request-ID") {
					t.Fatalf("invalid error: %v", body)
				}
			}
		})
	}
	if strings.Contains(logs.String(), "hidden-") {
		t.Fatalf("sensitive data in log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "request_id") {
		t.Fatal("missing request log")
	}
	db.Close()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed DB health status = %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "database is closed") {
		t.Fatal("leaked internal error")
	}
}

func TestHealthCanceledContext(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	NewHandler(db, slog.Default()).ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil).WithContext(ctx))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("canceled health status = %d", w.Code)
	}
}
