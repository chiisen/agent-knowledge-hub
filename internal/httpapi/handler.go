// Package httpapi provides the JSON HTTP boundary.
package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
)

const maxResponseBytes = 1 << 20

type responseBuffer struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func newResponseBuffer() *responseBuffer      { return &responseBuffer{header: make(http.Header)} }
func (w *responseBuffer) Header() http.Header { return w.header }
func (w *responseBuffer) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *responseBuffer) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.body.Len()+len(p) > maxResponseBytes {
		w.overflow = true
		return 0, fmt.Errorf("HTTP response exceeds %d bytes", maxResponseBytes)
	}
	return w.body.Write(p)
}
func (w *responseBuffer) commit(dst http.ResponseWriter, requestID string) {
	if w.overflow {
		w.header = make(http.Header)
		w.header.Set("Content-Type", "application/json")
		w.status = http.StatusInternalServerError
		w.body.Reset()
		_ = json.NewEncoder(&w.body).Encode(map[string]any{"error": map[string]string{
			"code": "response_too_large", "message": "Response exceeds the configured size limit.", "request_id": requestID,
		}})
	}
	for key, values := range w.header {
		dst.Header()[key] = append([]string(nil), values...)
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	_, _ = dst.Write(w.body.Bytes())
}

func NewHandler(db *sql.DB, logger *slog.Logger) http.Handler {
	return newHandler(db, nil, logger)
}

func NewKnowledgeHandler(db *sql.DB, service *knowledge.Service, logger *slog.Logger) http.Handler {
	return newHandler(db, service, logger)
}

func newHandler(db *sql.DB, service *knowledge.Service, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dst := w
		buffer := newResponseBuffer()
		w = buffer
		started := time.Now()
		id := rand.Text()
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusOK
		route := "unmatched"
		defer func() {
			if recovered := recover(); recovered != nil {
				status = http.StatusInternalServerError
				logger.ErrorContext(r.Context(), "HTTP handler panic recovered", "request_id", id, "panic_type", fmt.Sprintf("%T", recovered))
				buffer.body.Reset()
				buffer.status = 0
				buffer.header.Set("Content-Type", "application/json")
				buffer.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(buffer).Encode(map[string]any{"error": map[string]string{
					"code": "internal_error", "message": "An internal error occurred.", "request_id": id,
				}})
			}
			if buffer.overflow {
				status = http.StatusInternalServerError
			}
			logger.InfoContext(r.Context(), "HTTP request", "request_id", id,
				"method", r.Method, "route", route, "status", status, "duration_ms", time.Since(started).Milliseconds())
			buffer.commit(dst, id)
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
