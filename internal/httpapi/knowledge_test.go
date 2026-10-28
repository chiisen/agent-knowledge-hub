package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/google/uuid"
)

const collection = "/api/v1/knowledge"
const createBody = `{"title":"title","content":"hidden-content","type":"decision","scope":"project","project":"alpha","module":"payments","source":"hidden-source"}`

func apiFixture(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	return NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), slog.New(slog.NewJSONHandler(&logs, nil))), &logs
}

func apiRequest(t *testing.T, h http.Handler, method, path, body, etag string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer hidden-token")
	if etag != "" {
		r.Header.Set("If-Match", etag)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request ID")
	}
	if want != 204 {
		if w.Header().Get("Content-Type") != "application/json" {
			t.Fatal("not JSON")
		}
		var value map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if want >= 400 {
			var e struct {
				Code, Message string
				RequestID     string `json:"request_id"`
			}
			if err := json.Unmarshal(value["error"], &e); err != nil || e.Code == "" || e.RequestID != w.Header().Get("X-Request-ID") {
				t.Fatalf("invalid error: %s", w.Body.String())
			}
		}
	}
	return w
}

func record(t *testing.T, w *httptest.ResponseRecorder) knowledge.Knowledge {
	t.Helper()
	var body struct{ Data knowledge.Knowledge }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}
func pageSize(t *testing.T, w *httptest.ResponseRecorder) (int, bool) {
	t.Helper()
	var body struct {
		Data       []knowledge.Knowledge
		Pagination struct {
			HasMore bool `json:"has_more"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data == nil {
		t.Fatal("list must use [] rather than null")
	}
	return len(body.Data), body.Pagination.HasMore
}

func TestKnowledgeAPIWorkflow(t *testing.T) {
	h, logs := apiFixture(t)
	created := apiRequest(t, h, "POST", collection, createBody, "", 201)
	k := record(t, created)
	path := collection + "/" + k.ID
	id, err := uuid.Parse(k.ID)
	if err != nil || id.Version() != 4 || created.Header().Get("ETag") != `"1"` || created.Header().Get("Location") != path {
		t.Fatalf("invalid created record: %+v", k)
	}
	if k.Status != "draft" || k.UsableAsRule || k.Version != 1 || k.CreatedAt.IsZero() || k.UpdatedAt.Before(k.CreatedAt) {
		t.Fatalf("invalid defaults: %+v", k)
	}
	apiRequest(t, h, "GET", path, "", "", 200)
	if n, _ := pageSize(t, apiRequest(t, h, "GET", collection, "", "", 200)); n != 0 {
		t.Fatal("draft leaked into default list")
	}
	if n, _ := pageSize(t, apiRequest(t, h, "GET", collection+"?status=draft", "", "", 200)); n != 1 {
		t.Fatal("draft missing")
	}
	confirmed := apiRequest(t, h, "PATCH", path, `{"status":"confirmed"}`, `"1"`, 200)
	if got := record(t, confirmed); !got.UsableAsRule || got.Version != 2 {
		t.Fatalf("not confirmed: %+v", got)
	}
	apiRequest(t, h, "PATCH", path, `{"title":"stale"}`, `"1"`, 412)
	apiRequest(t, h, "PATCH", path, `{"source":"new","status":"confirmed"}`, `"2"`, 422)
	changed := apiRequest(t, h, "PATCH", path, `{"content":"updated","module":null}`, `"2"`, 200)
	if got := record(t, changed); got.Status != "draft" || got.Module != nil || got.Version != 3 {
		t.Fatalf("invalid changed record: %+v", got)
	}
	apiRequest(t, h, "PATCH", path, `{"status":"deprecated"}`, `"3"`, 200)
	apiRequest(t, h, "PATCH", path, `{"status":"deprecated"}`, `"4"`, 200)
	apiRequest(t, h, "DELETE", path, "", `"4"`, 412)
	apiRequest(t, h, "DELETE", path, "", `"5"`, 204)
	apiRequest(t, h, "GET", path, "", "", 404)
	apiRequest(t, h, "PATCH", path, `{"title":"resurrect"}`, `"5"`, 404)
	apiRequest(t, h, "DELETE", path, "", `"5"`, 204)
	if n, _ := pageSize(t, apiRequest(t, h, "GET", collection+"?status=deprecated", "", "", 200)); n != 0 {
		t.Fatal("archived item leaked")
	}
	if strings.Contains(logs.String(), "hidden-") || strings.Contains(logs.String(), k.ID) {
		t.Fatalf("sensitive request data logged: %s", logs.String())
	}
}

func TestKnowledgeAPIValidation(t *testing.T) {
	h, _ := apiFixture(t)
	k := record(t, apiRequest(t, h, "POST", collection, createBody, "", 201))
	path := collection + "/" + k.ID
	missing := collection + "/3141887e-1a19-4108-ae44-2bc478928d29"
	for _, tc := range []struct {
		name, method, path, body, etag string
		status                         int
	}{
		{"invalid-id", "GET", collection + "/bad", "", "", 400},
		{"missing", "GET", missing, "", "", 404},
		{"missing-update", "PATCH", missing, `{"title":"x"}`, `"1"`, 404},
		{"missing-delete", "DELETE", missing, "", `"1"`, 404},
		{"malformed", "POST", collection, `{"title":`, "", 400},
		{"trailing", "POST", collection, createBody + `{}`, "", 400},
		{"null", "POST", collection, `null`, "", 400},
		{"missing-fields", "POST", collection, `{}`, "", 422},
		{"unknown-field", "POST", collection, `{"id":"client-id"}`, "", 422},
		{"wrong-type", "POST", collection, `{"title":3}`, "", 422},
		{"null-required", "PATCH", path, `{"content":null}`, `"1"`, 422},
		{"empty-patch", "PATCH", path, `{}`, `"1"`, 422},
		{"unknown-patch", "PATCH", path, `{"version":2}`, `"1"`, 422},
		{"no-condition", "PATCH", path, `{"title":"new"}`, "", 428},
		{"bad-condition", "PATCH", path, `{"title":"new"}`, `W/"1"`, 400},
		{"wildcard", "DELETE", path, "", "*", 400},
		{"no-delete-condition", "DELETE", path, "", "", 428},
		{"bad-status", "PATCH", path, `{"status":"bad"}`, `"1"`, 422},
		{"too-large-content", "PATCH", path, `{"content":"` + strings.Repeat("x", 65537) + `"}`, `"1"`, 422},
		{"too-large-body", "POST", collection, strings.Repeat("x", 131073), "", 413},
		{"bad-page", "GET", collection + "?limit=101", "", "", 400},
		{"negative-offset", "GET", collection + "?offset=-1", "", "", 400},
		{"bad-filter", "GET", collection + "?status=bad", "", "", 422},
		{"unknown-query", "GET", collection + "?include_archived=true", "", "", 400},
		{"duplicate-query", "GET", collection + "?limit=1&limit=2", "", "", 400},
		{"method", "PUT", path, `{}`, "", 405},
	} {
		t.Run(tc.name, func(t *testing.T) { apiRequest(t, h, tc.method, tc.path, tc.body, tc.etag, tc.status) })
	}
	r := httptest.NewRequest("POST", collection, strings.NewReader(createBody))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 415 {
		t.Fatalf("missing content-type status=%d", w.Code)
	}
}

func TestKnowledgeAPIFiltersAndPagination(t *testing.T) {
	h, _ := apiFixture(t)
	for _, body := range []string{
		strings.Replace(createBody, `"source":`, `"status":"confirmed","source":`, 1),
		strings.Replace(createBody, `"source":`, `"status":"confirmed","source":`, 1),
		strings.Replace(strings.Replace(createBody, `"alpha"`, `"beta"`, 1), `"source":`, `"status":"confirmed","source":`, 1),
	} {
		apiRequest(t, h, "POST", collection, body, "", 201)
	}
	queries := []struct {
		query string
		count int
		more  bool
	}{
		{"?project=alpha&module=payments&type=decision&status=confirmed&limit=1", 1, true},
		{"?project=alpha&limit=1&offset=1", 1, false},
		{"?project=alpha&limit=1&offset=2", 0, false},
		{"?project=beta", 1, false}, {"?module=other", 0, false},
		{"?type=other", 0, false}, {"?status=draft", 0, false},
		{"?project=alpha%27%20OR%201%3D1--", 0, false},
	}
	for _, tc := range queries {
		n, more := pageSize(t, apiRequest(t, h, "GET", collection+tc.query, "", "", 200))
		if n != tc.count || more != tc.more {
			t.Fatalf("%s: count=%d more=%v", tc.query, n, more)
		}
	}
	first := recordPage(t, apiRequest(t, h, "GET", collection+"?project=alpha&limit=1", "", "", 200))
	second := recordPage(t, apiRequest(t, h, "GET", collection+"?project=alpha&limit=1&offset=1", "", "", 200))
	if first[0].ID == second[0].ID {
		t.Fatal("offset returned the same record twice")
	}
}

func recordPage(t *testing.T, w *httptest.ResponseRecorder) []knowledge.Knowledge {
	t.Helper()
	var body struct{ Data []knowledge.Knowledge }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Data
}

func TestKnowledgeAPIMaximumPageSize(t *testing.T) {
	h, _ := apiFixture(t)
	for i := 0; i < 101; i++ {
		apiRequest(t, h, "POST", collection, createBody, "", 201)
	}
	for _, tc := range []struct {
		query string
		count int
		more  bool
	}{
		{"?status=draft", 20, true},
		{"?status=draft&limit=100", 100, true},
		{"?status=draft&limit=100&offset=100", 1, false},
	} {
		n, more := pageSize(t, apiRequest(t, h, "GET", collection+tc.query, "", "", 200))
		if n != tc.count || more != tc.more {
			t.Fatalf("%s: count=%d more=%v", tc.query, n, more)
		}
	}
}

func TestKnowledgeAPILoopbackIntegration(t *testing.T) {
	h, _ := apiFixture(t)
	server := httptest.NewServer(h)
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Post(server.URL+collection, "application/json", strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Data knowledge.Knowledge }
	err = json.NewDecoder(response.Body).Decode(&body)
	response.Body.Close()
	if err != nil || response.StatusCode != 201 || body.Data.ID == "" {
		t.Fatalf("POST status=%d err=%v", response.StatusCode, err)
	}
	request, err := http.NewRequest("DELETE", server.URL+collection+"/"+body.Data.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("If-Match", response.Header.Get("ETag"))
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("DELETE status=%d", response.StatusCode)
	}
	response, err = client.Get(server.URL + collection + "/" + body.Data.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 404 || response.Header.Get("X-Request-ID") == "" {
		t.Fatalf("archived GET status=%d", response.StatusCode)
	}
}

func TestKnowledgeAPICancellationAndDatabaseErrors(t *testing.T) {
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "knowledge.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), slog.New(slog.NewJSONHandler(&logs, nil)))
	k := record(t, apiRequest(t, h, "POST", collection, createBody, "", 201))
	for _, tc := range []struct{ method, path, body string }{
		{"GET", collection, ""}, {"GET", collection + "/" + k.ID, ""}, {"POST", collection, createBody},
		{"PATCH", collection + "/" + k.ID, `{"title":"new"}`}, {"DELETE", collection + "/" + k.ID, ""},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)).WithContext(ctx)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("If-Match", `"1"`)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("canceled %s = %d", tc.method, w.Code)
		}
	}
	// Occupy the only connection and ensure HTTP propagates a short deadline to SQL.
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", collection, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	conn.Close()
	if w.Code != 503 {
		t.Fatalf("pool timeout status=%d", w.Code)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_create BEFORE INSERT ON knowledge BEGIN SELECT RAISE(ABORT, 'hidden-database-secret'); END;`); err != nil {
		t.Fatal(err)
	}
	failure := apiRequest(t, h, "POST", collection, createBody, "", 500)
	if strings.Contains(failure.Body.String(), "hidden-") || strings.Contains(logs.String(), "hidden-") {
		t.Fatalf("leaked sensitive error: %s %s", failure.Body.String(), logs.String())
	}
	if !strings.Contains(logs.String(), failure.Header().Get("X-Request-ID")) {
		t.Fatal("error log missing request ID")
	}
}

func TestArchiveResponseHasNoBody(t *testing.T) {
	h, _ := apiFixture(t)
	k := record(t, apiRequest(t, h, "POST", collection, createBody, "", 201))
	w := apiRequest(t, h, "DELETE", collection+"/"+k.ID, "", `"1"`, 204)
	body, err := io.ReadAll(w.Result().Body)
	if err != nil || len(body) != 0 {
		t.Fatalf("204 body=%q err=%v", body, err)
	}
}
