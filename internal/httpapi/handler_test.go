package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

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
