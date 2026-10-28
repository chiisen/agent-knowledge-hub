// Package httpapi provides the JSON HTTP boundary.
package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
)

func NewHandler(db *sql.DB, logger *slog.Logger) http.Handler {
	return newHandler(db, nil, logger)
}

func NewKnowledgeHandler(db *sql.DB, service *knowledge.Service, logger *slog.Logger) http.Handler {
	return newHandler(db, service, logger)
}

func newHandler(db *sql.DB, service *knowledge.Service, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		id := rand.Text()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusOK
		route := "unmatched"
		defer func() {
			logger.InfoContext(r.Context(), "HTTP request", "request_id", id,
				"method", r.Method, "route", route, "status", status, "duration_ms", time.Since(started).Milliseconds())
		}()
		fail := func(code int, name, message string) {
			status = code
			w.WriteHeader(code)
			if err := json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code": name, "message": message, "request_id": id,
			}}); err != nil {
				logger.ErrorContext(r.Context(), "write error response", "request_id", id, "error", err)
			}
		}
		if service != nil && (r.URL.Path == knowledgePath || strings.HasPrefix(r.URL.Path, knowledgePath+"/")) {
			route = knowledgePath
			if r.URL.Path != knowledgePath {
				if r.URL.Path == knowledgePath+"/search" {
					route += "/search"
				} else {
					route += "/{id}"
				}
			}
			status = serveKnowledge(w, r, id, logger, service, fail)
			return
		}
		if r.URL.Path != "/healthz" {
			fail(http.StatusNotFound, "not_found", "Resource not found.")
			return
		}
		route = "/healthz"
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			fail(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := db.PingContext(ctx); err != nil {
			logger.ErrorContext(ctx, "health check database ping failed", "request_id", id, "error", err)
			fail(http.StatusServiceUnavailable, "database_unavailable", "Database is unavailable.")
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
			logger.ErrorContext(ctx, "write health response", "request_id", id, "error", err)
		}
	})
}
