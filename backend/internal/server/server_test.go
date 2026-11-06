package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"pixel-agent-office/backend/internal/store"
	"strings"
	"testing"
	"time"
)

type fakeProjects struct{}

func (fakeProjects) AddProject(_ context.Context, path string) (store.Project, error) {
	return store.Project{Path: path}, nil
}
func (fakeProjects) Projects(context.Context) ([]store.Project, error) { return []store.Project{}, nil }
func (fakeProjects) Task(context.Context, int64) (store.DetailSnapshot, error) {
	return store.DetailSnapshot{}, store.ErrNotFound
}
func (fakeProjects) CancelQueued(context.Context, int64) (store.Task, error) {
	return store.Task{}, store.ErrNotFound
}
func (fakeProjects) CurrentSequence(context.Context) (int64, error)            { return 0, nil }
func (fakeProjects) EventsAfter(context.Context, int64) ([]store.Event, error) { return nil, nil }

func TestLocalSessionBoundaries(t *testing.T) {
	s := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	s.codexAvailable = func() bool { return true }
	request := func(method, path, body, origin string) *http.Request {
		r := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	forbidden := httptest.NewRecorder()
	s.ServeHTTP(forbidden, request("POST", "/api/bootstrap", `{"secret":"secret"}`, "https://evil.example"))
	if forbidden.Code != 403 {
		t.Fatalf("cross origin: %d", forbidden.Code)
	}
	bootstrap := httptest.NewRecorder()
	s.ServeHTTP(bootstrap, request("POST", "/api/bootstrap", `{"secret":"secret"}`, "http://127.0.0.1:8080"))
	if bootstrap.Code != 200 {
		t.Fatalf("bootstrap: %d", bootstrap.Code)
	}
	cookies := bootstrap.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatal("session cookie protections missing")
	}
	again := httptest.NewRecorder()
	s.ServeHTTP(again, request("POST", "/api/bootstrap", `{"secret":"secret"}`, "http://127.0.0.1:8080"))
	if again.Code != 403 {
		t.Fatalf("reused secret: %d", again.Code)
	}
	mutate := request("POST", "/api/projects", `{"path":"/tmp"}`, "http://127.0.0.1:8080")
	mutate.AddCookie(bootstrap.Result().Cookies()[0])
	blocked := httptest.NewRecorder()
	s.ServeHTTP(blocked, mutate)
	if blocked.Code != 403 {
		t.Fatalf("missing csrf: %d", blocked.Code)
	}
}
func (fakeProjects) CreateTask(_ context.Context, projectID int64, prompt, provider string) (store.Task, error) {
	return store.Task{ProjectID: projectID, Prompt: prompt, Provider: provider, Status: "queued"}, nil
}
func (fakeProjects) Tasks(context.Context) (store.TaskSnapshot, error) {
	return store.TaskSnapshot{Tasks: []store.Task{}}, nil
}

func TestTaskSubmissionValidation(t *testing.T) {
	s := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	s.codexAvailable = func() bool { return true }
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"secret"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, login)
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"projectId":1,"prompt":""}`, 400},
		{`{"projectId":0,"prompt":"test"}`, 400},
		{`{"projectId":1,"prompt":"test"}`, 201},
	} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/tasks", strings.NewReader(tc.body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", body.CSRFToken)
		req.AddCookie(response.Result().Cookies()[0])
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s: got %d want %d", tc.body, rec.Code, tc.status)
		}
	}
}

type approvalProjects struct{ fakeProjects }

func (approvalProjects) Task(_ context.Context, id int64) (store.DetailSnapshot, error) {
	if id != 1 {
		return store.DetailSnapshot{}, store.ErrNotFound
	}
	return store.DetailSnapshot{Task: store.Task{ID: 1, Status: "waiting_for_user"}, PendingApproval: &store.Approval{ID: "valid", TaskID: 1, Status: "pending"}}, nil
}

type approvalResponder struct{ received string }

func (a *approvalResponder) Respond(_ context.Context, id int64, approvalID, decision string) error {
	if id != 1 || approvalID != "valid" {
		return store.ErrConflict
	}
	a.received = decision
	return nil
}
func TestApprovalHTTPRequiresMatchingPendingRequest(t *testing.T) {
	approvals := &approvalResponder{}
	s := New(approvalProjects{}, "127.0.0.1:8080", "secret", approvals, nil)
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"secret"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	session := httptest.NewRecorder()
	s.ServeHTTP(session, login)
	var auth struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/tasks/1/approvals/other", `{"decision":"approve"}`, 409},
		{"/api/tasks/1/approvals/valid", `{"decision":"invalid"}`, 400},
		{"/api/tasks/1/approvals/valid", `{"decision":"deny"}`, 200},
	} {
		req := httptest.NewRequest("POST", "http://127.0.0.1:8080"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", auth.CSRFToken)
		req.AddCookie(session.Result().Cookies()[0])
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Errorf("%s: %d want %d", tc.path, rec.Code, tc.status)
		}
	}
	if approvals.received != "deny" {
		t.Fatalf("decision %q", approvals.received)
	}
}

func TestStaticFrontendKeepsHostAndOriginChecks(t *testing.T) {
	s := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	s.codexAvailable = func() bool { return true }
	s.SetFrontend(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("app")) }))
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "app" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("frontend response %d %q", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("GET", "http://evil.test/", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("invalid host: %d", rec.Code)
	}
}

func TestUnavailableProviderCannotCreateTask(t *testing.T) {
	s := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	s.codexAvailable = func() bool { return true }
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"secret"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	session := httptest.NewRecorder()
	s.ServeHTTP(session, login)
	var auth struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(session.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/tasks", strings.NewReader(`{"projectId":1,"prompt":"test","provider":"antigravity"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("X-CSRF-Token", auth.CSRFToken)
	req.AddCookie(session.Result().Cookies()[0])
	result := httptest.NewRecorder()
	s.ServeHTTP(result, req)
	if result.Code != http.StatusConflict {
		t.Fatalf("unavailable provider returned %d", result.Code)
	}
	s.antigravityAvailable = func() bool { return true }
	s.codexAvailable = func() bool { return false }
	req = httptest.NewRequest("POST", "http://127.0.0.1:8080/api/tasks", strings.NewReader(`{"projectId":1,"prompt":"test","provider":"antigravity"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("X-CSRF-Token", auth.CSRFToken)
	req.AddCookie(session.Result().Cookies()[0])
	result = httptest.NewRecorder()
	s.ServeHTTP(result, req)
	if result.Code != http.StatusCreated {
		t.Fatalf("available provider returned %d", result.Code)
	}
	var task store.Task
	if err := json.Unmarshal(result.Body.Bytes(), &task); err != nil || task.Provider != "antigravity" {
		t.Fatalf("wrong provider: %#v %v", task, err)
	}
}

func TestAntigravityCapabilitiesFollowAvailability(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(fmt.Sprint(available), func(t *testing.T) {
			app := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
			if available {
				app.antigravityAvailable = func() bool { return true }
			}
			response := httptest.NewRecorder()
			app.listProviders(response, httptest.NewRequest("GET", "/api/providers", nil))
			var providers []struct {
				ID           string          `json:"id"`
				Available    bool            `json:"available"`
				Capabilities map[string]bool `json:"capabilities"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &providers); err != nil {
				t.Fatal(err)
			}
			for _, provider := range providers {
				if provider.ID == "antigravity" {
					if provider.Available != available || provider.Capabilities["cancellation"] != available || provider.Capabilities["approval"] {
						t.Fatalf("incorrect capabilities: %#v", provider)
					}
					return
				}
			}
			t.Fatal("Antigravity provider missing")
		})
	}
}

func TestExpiredBootstrapAndUnauthenticatedReads(t *testing.T) {
	app := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	app.expires = time.Now().Add(-time.Minute)
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"secret"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != http.StatusForbidden || len(response.Result().Cookies()) != 0 {
		t.Fatal("expired bootstrap accepted")
	}
	for _, path := range []string{"/api/session", "/api/projects", "/api/tasks", "/api/events", "/api/workflows", "/api/providers"} {
		for _, cookie := range []string{"", "unrecognised-session"} {
			req = httptest.NewRequest("GET", "http://127.0.0.1:8080"+path, nil)
			if cookie != "" {
				req.AddCookie(&http.Cookie{Name: "pixel_session", Value: cookie})
			}
			response = httptest.NewRecorder()
			app.ServeHTTP(response, req)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated %s: %d", path, response.Code)
			}
		}
	}
}

func TestProjectHTTPRejectsInaccessibleDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory access permissions")
	}
	path := t.TempDir()
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0700) })
	app := New(fakeProjects{}, "127.0.0.1:8080", "secret", nil, nil)
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"secret"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	auth := httptest.NewRecorder()
	app.ServeHTTP(auth, login)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(auth.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/projects", strings.NewReader(string(body)))
	request.Header.Set("Origin", "http://127.0.0.1:8080")
	request.Header.Set("X-CSRF-Token", session.CSRFToken)
	request.AddCookie(auth.Result().Cookies()[0])
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("inaccessible directory: %d", response.Code)
	}
}
