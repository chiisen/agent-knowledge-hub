package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/token"
)

const knowledgePath = "/api/v1/knowledge"

type httpError struct {
	status        int
	code, message string
}

func (e *httpError) Error() string    { return e.message }
func badRequest(message string) error { return &httpError{400, "invalid_request", message} }

func serveKnowledge(w http.ResponseWriter, r *http.Request, requestID string, logger *slog.Logger, svc *knowledge.Service, auth token.Authenticator, fail func(int, string, string)) int {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	failure := func(err error) int {
		status, code, message := 500, "internal_error", "An internal error occurred."
		var he *httpError
		var sqliteError interface{ Code() int }
		switch {
		case errors.As(err, &he):
			status, code, message = he.status, he.code, he.message
		case errors.Is(err, token.ErrUnauthenticated):
			status, code, message = 401, "unauthorized", "A valid bearer token is required."
		case errors.Is(err, knowledge.ErrInvalidID):
			status, code, message = 400, "invalid_uuid", "A canonical UUID is required."
		case errors.Is(err, knowledge.ErrInvalid):
			status, code, message = 422, "validation_failed", err.Error()
		case errors.Is(err, knowledge.ErrNotFound):
			status, code, message = 404, "not_found", "Knowledge not found."
		case errors.Is(err, knowledge.ErrConflict):
			status, code, message = 412, "version_conflict", "Knowledge changed; read its current ETag."
		case errors.Is(err, context.Canceled):
			status, code, message = 503, "request_canceled", "Request was canceled."
		case errors.Is(err, context.DeadlineExceeded):
			status, code, message = 503, "request_timeout", "Request deadline exceeded."
		case errors.As(err, &sqliteError) && (sqliteError.Code()&255 == 5 || sqliteError.Code()&255 == 6):
			status, code, message = 503, "database_busy", "Database is temporarily busy."
		}
		if status >= 500 {
			// Driver errors may contain sensitive trigger text. Log safe classification, not raw SQL/error text.
			attrs := []any{"request_id", requestID, "operation", r.Method, "error_kind", code}
			if errors.As(err, &sqliteError) {
				attrs = append(attrs, "sqlite_code", sqliteError.Code())
			}
			logger.ErrorContext(ctx, "knowledge operation failed", attrs...)
		}
		fail(status, code, message)
		return status
	}
	success := func(status int, data any) int {
		w.WriteHeader(status)
		if status != 204 {
			if err := json.NewEncoder(w).Encode(data); err != nil {
				logger.ErrorContext(ctx, "write knowledge response failed", "request_id", requestID, "error_kind", "response_write_failed")
			}
		}
		return status
	}
	if err := ctx.Err(); err != nil {
		return failure(err)
	}
	if auth == nil {
		return failure(token.ErrUnauthenticated)
	}
	if err := auth.Authenticate(ctx, r.Header.Get("Authorization"), time.Now()); err != nil {
		return failure(err)
	}
	isCollection := r.URL.Path == knowledgePath
	if r.URL.Path == knowledgePath+"/search" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			return failure(&httpError{405, "method_not_allowed", "Method not allowed."})
		}
		fields, err := readObject(w, r)
		if err != nil {
			return failure(err)
		}
		f := knowledge.Filter{Limit: 20, Status: "confirmed"}
		var query string
		for name, raw := range fields {
			var value string
			switch name {
			case "query":
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return failure(&httpError{422, "validation_failed", "query must be a string."})
				}
				if err := json.Unmarshal(raw, &query); err != nil {
					return failure(&httpError{422, "validation_failed", "query must be a string."})
				}
			case "project", "module", "type", "scope", "status":
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return failure(&httpError{422, "validation_failed", "Search filters must be non-empty strings."})
				}
				if err := json.Unmarshal(raw, &value); err != nil {
					return failure(&httpError{422, "validation_failed", "Search filters must be strings."})
				}
				if strings.TrimSpace(value) == "" {
					return failure(&httpError{422, "validation_failed", "Search filters must be non-empty strings."})
				}
				switch name {
				case "project":
					f.Project = value
				case "module":
					f.Module = value
				case "type":
					f.Type = value
				case "scope":
					f.Scope = value
				case "status":
					f.Status = value
				}
			case "limit", "offset":
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return failure(&httpError{422, "validation_failed", "limit and offset must be integers."})
				}
				var n int
				if err := json.Unmarshal(raw, &n); err != nil {
					return failure(&httpError{422, "validation_failed", "limit and offset must be integers."})
				}
				if name == "limit" {
					f.Limit = n
				} else {
					f.Offset = n
				}
			default:
				return failure(&httpError{422, "validation_failed", "Unknown search field."})
			}
		}
		page, err := svc.Search(ctx, query, f)
		if err != nil {
			return failure(err)
		}
		return success(200, map[string]any{"data": page.Items, "pagination": map[string]any{"limit": f.Limit, "offset": f.Offset, "has_more": page.HasMore}})
	}
	id := strings.TrimPrefix(r.URL.Path, knowledgePath+"/")
	if !isCollection && (id == "" || strings.Contains(id, "/")) {
		return failure(&httpError{404, "not_found", "Resource not found."})
	}
	if isCollection {
		switch r.Method {
		case http.MethodPost:
			fields, err := readObject(w, r)
			if err != nil {
				return failure(err)
			}
			p, err := decodePatch(fields)
			if err != nil {
				return failure(err)
			}
			input := knowledge.Create{Project: p.Project.Value, Module: p.Module.Value}
			if p.Title != nil {
				input.Title = *p.Title
			}
			if p.Content != nil {
				input.Content = *p.Content
			}
			if p.Type != nil {
				input.Type = *p.Type
			}
			if p.Scope != nil {
				input.Scope = *p.Scope
			}
			if p.Status != nil {
				input.Status = *p.Status
			}
			if p.Source != nil {
				input.Source = *p.Source
			}
			// An explicit empty status is invalid; only an omitted status defaults to draft.
			if p.Status != nil && *p.Status == "" {
				return failure(&httpError{422, "validation_failed", "status must not be empty."})
			}
			k, err := svc.Create(ctx, input)
			if err != nil {
				return failure(err)
			}
			w.Header().Set("Location", knowledgePath+"/"+k.ID)
			w.Header().Set("ETag", etag(k.Version))
			return success(201, map[string]any{"data": k})
		case http.MethodGet:
			f, err := parseFilter(r.URL.RawQuery)
			if err != nil {
				return failure(err)
			}
			page, err := svc.List(ctx, f)
			if err != nil {
				return failure(err)
			}
			return success(200, map[string]any{"data": page.Items, "pagination": map[string]any{"limit": f.Limit, "offset": f.Offset, "has_more": page.HasMore}})
		default:
			w.Header().Set("Allow", "GET, POST")
			return failure(&httpError{405, "method_not_allowed", "Method not allowed."})
		}
	}
	switch r.Method {
	case http.MethodGet:
		k, err := svc.Get(ctx, id)
		if err != nil {
			return failure(err)
		}
		w.Header().Set("ETag", etag(k.Version))
		return success(200, map[string]any{"data": k})
	case http.MethodPatch, http.MethodDelete:
		version, err := readVersion(r.Header)
		if err != nil {
			return failure(err)
		}
		if r.Method == http.MethodDelete {
			if err := svc.Archive(ctx, id, version); err != nil {
				return failure(err)
			}
			return success(204, nil)
		}
		fields, err := readObject(w, r)
		if err != nil {
			return failure(err)
		}
		p, err := decodePatch(fields)
		if err != nil {
			return failure(err)
		}
		k, err := svc.Update(ctx, id, version, p)
		if err != nil {
			return failure(err)
		}
		w.Header().Set("ETag", etag(k.Version))
		return success(200, map[string]any{"data": k})
	default:
		w.Header().Set("Allow", "GET, PATCH, DELETE")
		return failure(&httpError{405, "method_not_allowed", "Method not allowed."})
	}
}

func etag(version int64) string { return `"` + strconv.FormatInt(version, 10) + `"` }
func readVersion(header http.Header) (int64, error) {
	values := header.Values("If-Match")
	if len(values) == 0 || values[0] == "" {
		return 0, &httpError{428, "precondition_required", "If-Match is required."}
	}
	if len(values) != 1 {
		return 0, badRequest("A single version ETag is required.")
	}
	raw := strings.TrimSpace(values[0])
	v, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
	if err != nil || v < 1 || v == math.MaxInt64 || raw != etag(v) {
		return 0, badRequest("If-Match must contain one strong numeric ETag.")
	}
	return v, nil
}

func readObject(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, &httpError{415, "unsupported_media_type", "Content-Type must be application/json."}
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128*1024))
	if err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			return nil, &httpError{413, "body_too_large", "Request body exceeds 128 KiB."}
		}
		if r.Context().Err() != nil {
			return nil, r.Context().Err()
		}
		return nil, badRequest("Unable to read JSON body.")
	}
	if !utf8.Valid(body) {
		return nil, badRequest("JSON must be valid UTF-8.")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, badRequest("A single JSON object is required.")
	}
	return fields, nil
}

func decodePatch(fields map[string]json.RawMessage) (knowledge.Patch, error) {
	var p knowledge.Patch
	for name, raw := range fields {
		if name == "project" || name == "module" {
			field := knowledge.NullableString{Set: true}
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				var s string
				if err := json.Unmarshal(raw, &s); err != nil {
					return p, &httpError{422, "validation_failed", "project/module must be a string or null."}
				}
				field.Value = &s
			}
			if name == "project" {
				p.Project = field
			} else {
				p.Module = field
			}
			continue
		}
		var dest **string
		switch name {
		case "title":
			dest = &p.Title
		case "content":
			dest = &p.Content
		case "type":
			dest = &p.Type
		case "scope":
			dest = &p.Scope
		case "status":
			dest = &p.Status
		case "source":
			dest = &p.Source
		default:
			return p, &httpError{422, "validation_failed", "Unknown or immutable field."}
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return p, &httpError{422, "validation_failed", "Required fields cannot be null."}
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return p, &httpError{422, "validation_failed", "Knowledge fields must be strings."}
		}
		*dest = &s
	}
	return p, nil
}

func parseFilter(raw string) (knowledge.Filter, error) {
	f := knowledge.Filter{Limit: 20, Status: "confirmed"}
	query, err := url.ParseQuery(raw)
	if err != nil {
		return f, badRequest("Invalid query string.")
	}
	for name, values := range query {
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return f, badRequest("Query parameters must occur once and must not be empty.")
		}
		v := values[0]
		switch name {
		case "project":
			f.Project = v
		case "module":
			f.Module = v
		case "type":
			f.Type = v
		case "scope":
			f.Scope = v
		case "status":
			f.Status = v
		case "limit":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				return f, badRequest("limit must be between 1 and 100.")
			}
			f.Limit = n
		case "offset":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 10000 {
				return f, badRequest("offset must be between 0 and 10000.")
			}
			f.Offset = n
		default:
			return f, badRequest("Unsupported query parameter.")
		}
	}
	return f, nil
}
