package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"os/exec"
	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/orchestrator"
	"pixel-agent-office/backend/internal/store"
	"pixel-agent-office/backend/internal/testdb"
)

type recordingCanceller struct{ taskID int64 }

func (c *recordingCanceller) RequestCancel(id int64) bool { c.taskID = id; return true }

func TestWorkflowHTTPCreateAndCancel(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	app := New(db, "127.0.0.1:8080", "fixture", nil, nil)
	app.codexAvailable = func() bool { return true }
	send := func(method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		return response
	}
	login := send("POST", "/api/bootstrap", `{"secret":"fixture"}`, "", nil)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	body := fmt.Sprintf(`{"projectId":%d,"steps":[{"provider":"codex","prompt":"one"},{"provider":"codex","prompt":"two"}]}`, project.ID)
	if unauthorized := send("POST", "/api/workflows", body, "", cookie); unauthorized.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF: %d", unauthorized.Code)
	}
	mixed := fmt.Sprintf(`{"projectId":%d,"steps":[{"provider":"codex","prompt":"one"},{"provider":"antigravity","prompt":"two"}]}`, project.ID)
	if unavailable := send("POST", "/api/workflows", mixed, session.CSRFToken, cookie); unavailable.Code != http.StatusConflict {
		t.Fatalf("unavailable workflow provider: %d", unavailable.Code)
	}
	created := send("POST", "/api/workflows", body, session.CSRFToken, cookie)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var workflow store.CreatedWorkflow
	if err = json.Unmarshal(created.Body.Bytes(), &workflow); err != nil {
		t.Fatal(err)
	}
	list := send("GET", "/api/workflows", "", "", cookie)
	var all store.WorkflowList
	if err = json.Unmarshal(list.Body.Bytes(), &all); err != nil || list.Code != 200 || len(all.Workflows) != 1 {
		t.Fatalf("list %d %v", list.Code, err)
	}
	detail := send("GET", fmt.Sprintf("/api/workflows/%d", workflow.ID), "", "", cookie)
	var snapshot store.WorkflowSnapshot
	if err = json.Unmarshal(detail.Body.Bytes(), &snapshot); err != nil || detail.Code != 200 || len(snapshot.Steps) != 2 || snapshot.Steps[0].TaskID == nil {
		t.Fatalf("detail %d %v", detail.Code, err)
	}
	cancel := send("POST", fmt.Sprintf("/api/workflows/%d/cancel", workflow.ID), "{}", session.CSRFToken, cookie)
	if err = json.Unmarshal(cancel.Body.Bytes(), &snapshot); err != nil || cancel.Code != 200 || snapshot.Status != "cancelled" {
		t.Fatalf("cancel %d %v", cancel.Code, err)
	}
	created = send("POST", "/api/workflows", body, session.CSRFToken, cookie)
	if err = json.Unmarshal(created.Body.Bytes(), &workflow); err != nil || created.Code != http.StatusCreated {
		t.Fatalf("second create %d %v", created.Code, err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	controller := &recordingCanceller{}
	app.canceller = controller
	cancel = send("POST", fmt.Sprintf("/api/workflows/%d/cancel", workflow.ID), "{}", session.CSRFToken, cookie)
	if err = json.Unmarshal(cancel.Body.Bytes(), &snapshot); err != nil || cancel.Code != 200 || snapshot.Status != "running" || !snapshot.CancelRequested || controller.taskID != workflow.FirstTaskID {
		t.Fatalf("active cancel %d %v", cancel.Code, err)
	}
}

func TestWorkflowHTTPHandoffEndToEnd(t *testing.T) {
	for _, providers := range [][2]string{{"codex", "codex"}, {"codex", "antigravity"}, {"antigravity", "antigravity"}} {
		t.Run(providers[0]+"_to_"+providers[1], func(t *testing.T) {
			testWorkflowHTTPHandoff(t, providers[0], providers[1])
		})
	}
}

func testWorkflowHTTPHandoff(t *testing.T, firstProvider, secondProvider string) {
	t.Setenv("OPENAI_API_KEY", "workflow-test-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db, err := store.Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	prompts := make(chan string, 2)
	runner := func(provider string) orchestrator.Execute {
		return func(_ context.Context, _ int64, _, prompt string, _ <-chan struct{}) (codex.Execution, error) {
			prompts <- provider + ":" + prompt
			if prompt == "one" {
				return codex.Execution{Result: "first result workflow-test-secret", Terminated: true}, nil
			}
			return codex.Execution{Result: "final result", Terminated: true}, nil
		}
	}
	worker := orchestrator.NewWithAntigravity(db, runner("codex"), runner("antigravity"))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	app := New(db, "127.0.0.1:8080", "fixture", nil, worker)
	app.codexAvailable = func() bool { return firstProvider == "codex" || secondProvider == "codex" }
	app.antigravityAvailable = func() bool { return firstProvider == "antigravity" || secondProvider == "antigravity" }
	send := func(method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		return response
	}
	login := send("POST", "/api/bootstrap", `{"secret":"fixture"}`, "", nil)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	body := fmt.Sprintf(`{"projectId":%d,"steps":[{"provider":"%s","prompt":"one"},{"provider":"%s","prompt":"two"}]}`, project.ID, firstProvider, secondProvider)
	response := send("POST", "/api/workflows", body, session.CSRFToken, cookie)
	var created store.CreatedWorkflow
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d", response.Code)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("workflow timed out")
		case <-time.After(50 * time.Millisecond):
		}
		response = send("GET", fmt.Sprintf("/api/workflows/%d", created.ID), "", "", cookie)
		var snapshot store.WorkflowSnapshot
		if response.Code != http.StatusOK {
			t.Fatalf("snapshot: %d", response.Code)
		}
		if err = json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Status == "failed" {
			t.Fatalf("workflow failed: %v", snapshot.Error)
		}
		if snapshot.Status == "completed" {
			if len(snapshot.Steps) != 2 || snapshot.Steps[1].Result == nil || *snapshot.Steps[1].Result != "final result" {
				t.Fatalf("snapshot: %#v", snapshot)
			}
			first, second := <-prompts, <-prompts
			if first != firstProvider+":one" || !strings.Contains(second, "first result [REDACTED]") || strings.Contains(second, "workflow-test-secret") || !strings.HasPrefix(second, secondProvider+":two") {
				t.Fatalf("handoff prompts: %q %q", first, second)
			}
			return
		}
	}
}

func TestWorkflowHTTPCodexHandoff(t *testing.T) {
	if os.Getenv("CODEX_WORKFLOW_SMOKE") != "1" {
		t.Skip("set CODEX_WORKFLOW_SMOKE=1 to use Codex")
	}
	testWorkflowHTTPRealHandoff(t, "codex")
}

func TestWorkflowHTTPMixedOAuthHandoff(t *testing.T) {
	if os.Getenv("AGY_OAUTH_WORKFLOW_SMOKE") != "1" {
		t.Skip("set AGY_OAUTH_WORKFLOW_SMOKE=1 to use Codex and AI Pro quota")
	}
	testWorkflowHTTPRealHandoff(t, "antigravity")
}

func testWorkflowHTTPRealHandoff(t *testing.T, secondProvider string) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	db, err := store.Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	approvals := orchestrator.NewApprovals(db)
	worker := orchestrator.New(db, orchestrator.ExecuteCodex(approvals, db))
	if secondProvider == "antigravity" {
		runtime, err := antigravity.LoadOAuthRuntime(ctx)
		if err != nil {
			t.Fatal("OAuth runtime is not configured")
		}
		worker = orchestrator.NewWithAntigravity(db, orchestrator.ExecuteCodex(approvals, db), orchestrator.ExecuteAntigravityOAuth(db, runtime.DaemonID, runtime.ImageID, runtime.Volume))
	}
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		if secondProvider == "antigravity" {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			rows, err := db.Pool.Query(cleanupCtx, `SELECT container_id FROM container_executions WHERE container_id IS NOT NULL`)
			if err != nil {
				t.Error("cannot find temporary workflow containers")
				return
			}
			defer rows.Close()
			for rows.Next() {
				var id string
				if err = rows.Scan(&id); err != nil {
					t.Error(err)
					return
				}
				if err = exec.CommandContext(cleanupCtx, "docker", "rm", "-f", id).Run(); err != nil {
					t.Error("cannot remove temporary workflow container")
				}
			}
		}
	})
	app := New(db, "127.0.0.1:8080", "fixture", nil, worker)
	app.codexAvailable = func() bool { return true }
	app.antigravityAvailable = func() bool { return secondProvider == "antigravity" }
	send := func(method, path, body, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", csrf)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		return response
	}
	login := send("POST", "/api/bootstrap", `{"secret":"fixture"}`, "", nil)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	body := fmt.Sprintf(`{"projectId":%d,"steps":[{"provider":"codex","prompt":"Reply with exactly GUILD42. Do not use tools."},{"provider":"%s","prompt":"Repeat the previous step result exactly. Do not use tools."}]}`, project.ID, secondProvider)
	response := send("POST", "/api/workflows", body, session.CSRFToken, cookie)
	var created store.CreatedWorkflow
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d", response.Code)
	}
	if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("workflow timed out")
		case <-time.After(250 * time.Millisecond):
		}
		response = send("GET", fmt.Sprintf("/api/workflows/%d", created.ID), "", "", cookie)
		var snapshot store.WorkflowSnapshot
		if response.Code != http.StatusOK {
			t.Fatalf("snapshot: %d", response.Code)
		}
		if err = json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Status == "failed" {
			t.Fatalf("workflow failed: %v", snapshot.Error)
		}
		if snapshot.Status == "completed" {
			if len(snapshot.Steps) != 2 || snapshot.Steps[0].Result == nil || snapshot.Steps[1].Result == nil || strings.TrimSpace(*snapshot.Steps[0].Result) != "GUILD42" || !strings.Contains(*snapshot.Steps[1].Result, "GUILD42") {
				t.Fatalf("unexpected workflow results: %#v", snapshot.Steps)
			}
			return
		}
	}
}
