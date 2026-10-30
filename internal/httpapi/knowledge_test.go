package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chiisen/agent-knowledge-hub/internal/knowledge"
	"github.com/chiisen/agent-knowledge-hub/internal/sqlite"
	"github.com/google/uuid"
)

const testToken = "hidden-token"

func seedTestToken(t testing.TB, db *sql.DB) {
	t.Helper()
	sum := sha256.Sum256([]byte(testToken))
	if _, err := db.ExecContext(context.Background(), `INSERT INTO api_tokens(id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		"11111111-1111-4111-8111-111111111111", "test", hex.EncodeToString(sum[:]), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"alpha", "beta", "gamma"} {
		if _, err := db.ExecContext(context.Background(), `INSERT INTO api_token_projects(token_id, project) VALUES (?, ?)`,
			"11111111-1111-4111-8111-111111111111", project); err != nil {
			t.Fatal(err)
		}
	}
}

func authorize(r *http.Request) { r.Header.Set("Authorization", "Bearer "+testToken) }

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
	seedTestToken(t, db)
	var logs bytes.Buffer
	return NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), sqlite.NewTokenStore(db), slog.New(slog.NewJSONHandler(&logs, nil))), &logs
}

func TestHTTPConcurrentCreateAndUpdate(t *testing.T) {
	h, _ := apiFixture(t)
	server := httptest.NewServer(h)
	defer server.Close()
	const clients = 24
	client := &http.Client{Timeout: 10 * time.Second}
	paths := make([]string, clients)
	createLatency := make([]time.Duration, clients)
	var wg sync.WaitGroup
	errCh := make(chan error, clients)
	started := time.Now()
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			requestStarted := time.Now()
			request, err := http.NewRequest(http.MethodPost, server.URL+collection, strings.NewReader(strings.Replace(createBody, `"title":"title"`, fmt.Sprintf(`"title":"title-%d"`, i), 1)))
			if err != nil {
				errCh <- err
				return
			}
			request.Header.Set("Content-Type", "application/json")
			authorize(request)
			response, err := client.Do(request)
			if err != nil {
				errCh <- err
				return
			}
			var payload struct {
				Data knowledge.Knowledge `json:"data"`
			}
			err = json.NewDecoder(response.Body).Decode(&payload)
			response.Body.Close()
			if err != nil {
				errCh <- err
				return
			}
			if response.StatusCode != http.StatusCreated {
				errCh <- fmt.Errorf("create status=%d", response.StatusCode)
				return
			}
			paths[i] = collection + "/" + payload.Data.ID
			createLatency[i] = time.Since(requestStarted)
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	createElapsed := time.Since(started)
	updateLatency := make([]time.Duration, clients)
	started = time.Now()
	errCh = make(chan error, clients)
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			requestStarted := time.Now()
			request, err := http.NewRequest(http.MethodPatch, server.URL+path, strings.NewReader(`{"content":"concurrent update"}`))
			if err != nil {
				errCh <- err
				return
			}
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("If-Match", `"1"`)
			authorize(request)
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("update status=%d", response.StatusCode)
				}
			}
			if err != nil {
				errCh <- err
			} else {
				updateLatency[i] = time.Since(requestStarted)
			}
		}(i, path)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	updateElapsed := time.Since(started)
	readLatency := make([]time.Duration, clients)
	started = time.Now()
	errCh = make(chan error, clients)
	for i, path := range paths {
		wg.Add(1)
		go func(i int, path string) {
			defer wg.Done()
			requestStarted := time.Now()
			request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
			if err != nil {
				errCh <- err
				return
			}
			authorize(request)
			response, err := client.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					err = fmt.Errorf("read status=%d", response.StatusCode)
				}
			}
			if err != nil {
				errCh <- err
			} else {
				readLatency[i] = time.Since(requestStarted)
			}
		}(i, path)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	readElapsed := time.Since(started)
	t.Logf("environment=%s/%s %s clients=%d rows=%d create throughput=%.1f req/s p50=%s p95=%s; update throughput=%.1f req/s p50=%s p95=%s; read throughput=%.1f req/s p50=%s p95=%s",
		runtime.GOOS, runtime.GOARCH, runtime.Version(), clients, clients,
		float64(clients)/createElapsed.Seconds(), percentile(createLatency, 50), percentile(createLatency, 95),
		float64(clients)/updateElapsed.Seconds(), percentile(updateLatency, 50), percentile(updateLatency, 95),
		float64(clients)/readElapsed.Seconds(), percentile(readLatency, 50), percentile(readLatency, 95))
}

func percentile(values []time.Duration, p int) time.Duration {
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := (p*len(ordered)+99)/100 - 1
	if index < 0 {
		index = 0
	}
	return ordered[index]
}

func TestHTTPBusyDatabaseReturns503(t *testing.T) {
	db, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "busy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	seedTestToken(t, db)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), sqlite.NewTokenStore(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, collection, strings.NewReader(createBody))
	r.Header.Set("Content-Type", "application/json")
	authorize(r)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "database_busy") {
		t.Fatalf("busy response = %d %s", w.Code, w.Body.String())
	}
}

type deadlineRepository struct{ *sqlite.Repository }

func (deadlineRepository) List(ctx context.Context, _ knowledge.Filter) (knowledge.Page, error) {
	<-ctx.Done()
	return knowledge.Page{}, ctx.Err()
}

func TestKnowledgeRequestDeadlineReturns503(t *testing.T) {
	var logs bytes.Buffer
	h := NewKnowledgeHandler(nil, knowledge.NewService(&deadlineRepository{sqlite.NewRepository(nil)}), allowAuthenticator{}, slog.New(slog.NewJSONHandler(&logs, nil)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, collection, nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "request_timeout") {
		t.Fatalf("deadline response = %d %s", w.Code, w.Body.String())
	}
}

func BenchmarkKnowledgeList(b *testing.B) {
	db, err := sqlite.Open(context.Background(), filepath.Join(b.TempDir(), "benchmark.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	if err := sqlite.Migrate(context.Background(), db); err != nil {
		b.Fatal(err)
	}
	seedTestToken(b, db)
	svc := knowledge.NewService(sqlite.NewRepository(db))
	for i := 0; i < 100; i++ {
		if _, err := svc.Create(context.Background(), knowledge.Create{Title: fmt.Sprintf("title-%d", i), Content: "benchmark content", Type: "decision", Scope: "project", Status: "confirmed", Source: "benchmark"}); err != nil {
			b.Fatal(err)
		}
	}
	h := NewKnowledgeHandler(db, svc, sqlite.NewTokenStore(db), slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		request := httptest.NewRequest(http.MethodGet, collection, nil)
		authorize(request)
		h.ServeHTTP(httptest.NewRecorder(), request)
	}
}

func apiRequest(t *testing.T, h http.Handler, method, path, body, etag string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	authorize(r)
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

func TestKnowledgeSearchAPI(t *testing.T) {
	h, logs := apiFixture(t)
	base := strings.Replace(createBody, `"content":"hidden-content"`, `"content":"SQLite storage keeps transactions small"`, 1)
	for _, body := range []string{
		strings.Replace(strings.Replace(base, `"title":"title"`, `"title":"SQLite trigram"`, 1), `"source":`, `"status":"confirmed","source":`, 1),
		strings.Replace(strings.Replace(base, `"title":"title"`, `"title":"SQLite draft"`, 1), `"alpha"`, `"beta"`, 1),
		strings.Replace(strings.Replace(strings.Replace(base, `"title":"title"`, `"title":"SQLite second"`, 1), `"alpha"`, `"beta"`, 1), `"source":`, `"status":"confirmed","source":`, 1),
		strings.Replace(strings.Replace(strings.Replace(base, `"title":"title"`, `"title":"SQLite deprecated"`, 1), `"alpha"`, `"gamma"`, 1), `"source":`, `"status":"deprecated","source":`, 1),
	} {
		apiRequest(t, h, "POST", collection, body, "", 201)
	}
	w := apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite"}`, "", 200)
	if n, _ := pageSize(t, w); n != 2 {
		t.Fatalf("default status search count=%d", n)
	}
	w = apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite","status":"draft","project":"beta"}`, "", 200)
	if n, _ := pageSize(t, w); n != 1 {
		t.Fatalf("explicit draft search count=%d", n)
	}
	if n, _ := pageSize(t, apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite","status":"deprecated"}`, "", 200)); n != 1 {
		t.Fatalf("explicit deprecated search count=%d", n)
	}
	apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite*'quoted'"}`, "", 200)
	operatorQuery := apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite AND storage"}`, "", 200)
	if n, _ := pageSize(t, operatorQuery); n != 0 {
		t.Fatal("FTS operator text was executed instead of searched literally")
	}
	apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite\"quoted"}`, "", 200)
	var result struct {
		Data       []knowledge.SearchResult
		Pagination struct {
			HasMore bool `json:"has_more"`
		}
	}
	w = apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite storage","project":"alpha","module":"payments","type":"decision","scope":"project","status":"confirmed","limit":1}`, "", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || result.Data[0].Title != "SQLite trigram" || result.Data[0].Summary == "" || result.Pagination.HasMore {
		t.Fatalf("unexpected project-scoped result: %+v", result)
	}
	w = apiRequest(t, h, "POST", collection+"/search", `{"query":"SQLite","limit":1}`, "", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Data) != 1 || !result.Pagination.HasMore {
		t.Fatalf("search pagination = %+v", result)
	}
	if n, _ := pageSize(t, apiRequest(t, h, "POST", collection+"/search", `{"query":"不存在詞"}`, "", 200)); n != 0 {
		t.Fatal("no-result search returned data")
	}
	apiRequest(t, h, "POST", collection+"/search", `{"query":"secret-sensitive-search"}`, "", 200)
	if strings.Contains(logs.String(), "secret-sensitive-search") {
		t.Fatal("search query leaked to logs")
	}
	for _, body := range []string{`{}`, `{"query":"中"}`, `{"query":"foo OR bar"}`, `{"query":"SQLite","status":"bad"}`, `{"query":"SQLite","limit":101}`, `{"query":"SQLite","unknown":1}`, `{"query":null}`, `{"query":"SQLite","limit":null}`, `{"query":"SQLite","project":" "}`} {
		apiRequest(t, h, "POST", collection+"/search", body, "", 422)
	}
	apiRequest(t, h, "POST", collection+"/search", `{`, "", 400)
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
	authorize(r)
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
	post, err := http.NewRequest(http.MethodPost, server.URL+collection, strings.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	post.Header.Set("Content-Type", "application/json")
	authorize(post)
	response, err := client.Do(post)
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
	authorize(request)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("DELETE status=%d", response.StatusCode)
	}
	get, err := http.NewRequest(http.MethodGet, server.URL+collection+"/"+body.Data.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	authorize(get)
	response, err = client.Do(get)
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
	seedTestToken(t, db)
	var logs bytes.Buffer
	h := NewKnowledgeHandler(db, knowledge.NewService(sqlite.NewRepository(db)), sqlite.NewTokenStore(db), slog.New(slog.NewJSONHandler(&logs, nil)))
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
		authorize(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatalf("canceled %s = %d", tc.method, w.Code)
		}
	}
	// Occupy the bounded pool and ensure HTTP propagates a short deadline to SQL.
	conns := make([]*sql.Conn, db.Stats().MaxOpenConnections)
	for i := range conns {
		conns[i], err = db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest("GET", collection, nil).WithContext(ctx)
	authorize(r)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	for _, conn := range conns {
		conn.Close()
	}
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
