package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/orchestrator"
	"pixel-agent-office/backend/internal/store"
	"pixel-agent-office/backend/internal/testdb"
)

func TestHTTPTaskCancellationWithDatabase(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM approvals WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM events WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM tasks WHERE project_id=$1`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM projects WHERE id=$1`, project.ID)
	})
	started := make(chan struct{}, 1)
	worker := orchestrator.New(db, func(ctx context.Context, _ int64, _, _ string, stop <-chan struct{}) (codex.Execution, error) {
		started <- struct{}{}
		select {
		case <-stop:
			return codex.Execution{Terminated: true}, codex.ErrInterrupted
		case <-ctx.Done():
			return codex.Execution{}, ctx.Err()
		}
	})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	app := New(db, "127.0.0.1:8080", "test-secret", nil, worker)
	app.codexAvailable = func() bool { return true }
	send := func(method, path, body, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
		req.Header.Set("Origin", "http://127.0.0.1:8080")
		req.Header.Set("X-CSRF-Token", token)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		app.ServeHTTP(recorder, req)
		return recorder
	}
	login := send("POST", "/api/bootstrap", `{"secret":"test-secret"}`, "", nil)
	if login.Code != 200 {
		t.Fatalf("bootstrap: %d", login.Code)
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	body, err := json.Marshal(map[string]any{"projectId": project.ID, "prompt": "test task"})
	if err != nil {
		t.Fatal(err)
	}
	created := send("POST", "/api/tasks", string(body), session.CSRFToken, cookie)
	if created.Code != 201 {
		t.Fatalf("create: %d", created.Code)
	}
	var task store.Task
	if err := json.Unmarshal(created.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(4 * time.Second):
		t.Fatal("task was not dispatched")
	}
	if err := db.AppendActivity(context.Background(), task.ID, "started", "commandExecution", nil); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_API_KEY", "fixture-secret")
	if err := db.AppendActivity(context.Background(), task.ID, "started", "subAgentActivity", map[string]string{"agentThreadId": "child-fixture-secret", "parentThreadId": "parent", "kind": "started", "prompt": "private"}); err != nil {
		t.Fatal(err)
	}
	detailBefore, err := db.Task(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundActivity := false
	foundSubagent := false
	for _, event := range detailBefore.Events {
		if event.Type == "activity" && strings.Contains(string(event.Payload), "commandExecution") {
			foundActivity = true
		}
		if event.Type == "activity" && strings.Contains(string(event.Payload), "subAgentActivity") {
			payload := string(event.Payload)
			if strings.Contains(payload, "fixture-secret") || strings.Contains(payload, "private") || !strings.Contains(payload, "parent") {
				t.Fatal("subagent metadata was not filtered")
			}
			foundSubagent = true
		}
	}
	if !foundActivity {
		t.Fatal("missing activity event")
	}
	if !foundSubagent {
		t.Fatal("missing subagent activity event")
	}
	cancelled := send("POST", fmt.Sprintf("/api/tasks/%d/cancel", task.ID), "{}", session.CSRFToken, cookie)
	if cancelled.Code != 200 {
		t.Fatalf("cancel request: %d", cancelled.Code)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		detail, err := db.Task(context.Background(), task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Task.Status == "cancelled" {
			if len(detail.Events) < 4 {
				t.Fatal("missing lifecycle events")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	detail, err := db.Task(context.Background(), task.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	t.Fatalf("task not cancelled: %s", detail.Task.Status)
}

type approvalIntegrationClient struct{ replies chan string }

func (c *approvalIntegrationClient) Reply(_ json.RawMessage, response any) error {
	c.replies <- response.(map[string]string)["decision"]
	return nil
}
func TestHTTPApprovalFlowWithDatabase(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup := context.Background()
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM approvals WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM events WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM tasks WHERE project_id=$1`, project.ID)
		_, _ = db.Pool.Exec(cleanup, `DELETE FROM projects WHERE id=$1`, project.ID)
	})
	task, err := db.CreateTask(ctx, project.ID, "test", "codex")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != task.ID {
		t.Fatalf("claim: %v", err)
	}
	approvals := orchestrator.NewApprovals(db)
	client := &approvalIntegrationClient{replies: make(chan string, 1)}
	done := make(chan error, 1)
	go func() {
		done <- approvals.Handle(ctx, task.ID, client, codex.Message{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"echo test","cwd":"/tmp","reason":"test"}`)})
	}()
	var detail store.DetailSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		detail, err = db.Task(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.PendingApproval != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if detail.PendingApproval == nil {
		t.Fatal("approval was not persisted")
	}
	app := New(db, "127.0.0.1:8080", "test-secret", approvals, nil)
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"test-secret"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	loginResult := httptest.NewRecorder()
	app.ServeHTTP(loginResult, login)
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(loginResult.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:8080/api/tasks/%d/approvals/%s", task.ID, detail.PendingApproval.ID), strings.NewReader(`{"decision":"deny"}`))
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("X-CSRF-Token", session.CSRFToken)
	req.AddCookie(loginResult.Result().Cookies()[0])
	response := httptest.NewRecorder()
	app.ServeHTTP(response, req)
	if response.Code != 200 {
		t.Fatalf("approval HTTP status %d", response.Code)
	}
	if decision := <-client.replies; decision != "decline" {
		t.Fatalf("unexpected decision %q", decision)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	detail, err = db.Task(ctx, task.ID)
	if err != nil || detail.Task.Status != "running" || detail.PendingApproval != nil {
		t.Fatalf("approval resolution: %#v %v", detail, err)
	}
}

func TestHTTPCodexTaskEndToEnd(t *testing.T) {
	if os.Getenv("CODEX_SMOKE_TEST") != "1" {
		t.Skip("set CODEX_SMOKE_TEST=1 to use real Codex")
	}
	databaseURL := testdb.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	db, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	projectPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, projectPath)
	if err != nil {
		t.Fatal(err)
	}
	approvals := orchestrator.NewApprovals(db)
	worker := orchestrator.New(db, orchestrator.ExecuteCodex(approvals, db))
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	app := New(db, "127.0.0.1:8080", "test-secret", approvals, worker)
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
	login := send("POST", "/api/bootstrap", `{"secret":"test-secret"}`, "", nil)
	if login.Code != 200 {
		t.Fatalf("bootstrap HTTP %d", login.Code)
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(login.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	prompt := "Reply with the single word OK. Do not use any tools."
	if os.Getenv("CODEX_SMOKE_SUBAGENT") == "1" {
		prompt = "Spawn a subagent to answer what is 2+2, wait for its result, and report it. Do not read or write files."
	}
	body, err := json.Marshal(map[string]any{"projectId": project.ID, "prompt": prompt})
	if err != nil {
		t.Fatal(err)
	}
	cookie := login.Result().Cookies()[0]
	response := send("POST", "/api/tasks", string(body), session.CSRFToken, cookie)
	if response.Code != 201 {
		t.Fatalf("create HTTP %d", response.Code)
	}
	var task store.Task
	if err = json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case <-ctx.Done():
			t.Fatal("timed out waiting for Codex")
		case <-time.After(250 * time.Millisecond):
		}
		response = send("GET", fmt.Sprintf("/api/tasks/%d", task.ID), "", session.CSRFToken, cookie)
		if response.Code != 200 {
			t.Fatalf("detail HTTP %d", response.Code)
		}
		var detail store.DetailSnapshot
		if err = json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Task.Status == "failed" {
			if detail.Task.Error != nil {
				t.Fatalf("task failed: %s", *detail.Task.Error)
			}
			t.Fatal("task failed")
		}
		if detail.Task.Status == "completed" {
			if detail.Task.Result == nil || strings.TrimSpace(*detail.Task.Result) == "" {
				t.Fatal("missing result")
			}
			activity := false
			subagent := false
			for _, event := range detail.Events {
				if event.Type == "activity" {
					activity = true
					var payload map[string]string
					if json.Unmarshal(event.Payload, &payload) == nil && payload["itemType"] == "subAgentActivity" && payload["agentThreadId"] != "" && payload["parentThreadId"] != "" && payload["kind"] != "" {
						subagent = true
					}
				}
			}
			if !activity {
				t.Fatal("no activity events")
			}
			if os.Getenv("CODEX_SMOKE_SUBAGENT") == "1" && !subagent {
				t.Fatal("no subagent metadata received")
			}
			return
		}
	}
}

func TestHTTPResultEventIsRedactedAndOrdered(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "event-fixture-secret")
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
	worker := orchestrator.New(db, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{Result: "answer event-fixture-secret", Terminated: true}, nil
	})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	app := New(db, "127.0.0.1:8080", "fixture", nil, worker)
	app.codexAvailable = func() bool { return true }
	login := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/bootstrap", strings.NewReader(`{"secret":"fixture"}`))
	login.Header.Set("Origin", "http://127.0.0.1:8080")
	loggedIn := httptest.NewRecorder()
	app.ServeHTTP(loggedIn, login)
	if loggedIn.Code != http.StatusOK {
		t.Fatalf("bootstrap: %d", loggedIn.Code)
	}
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err = json.Unmarshal(loggedIn.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	cookie := loggedIn.Result().Cookies()[0]
	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()
	request := httptest.NewRequest("GET", "http://127.0.0.1:8080/api/events", nil).WithContext(streamCtx)
	request.AddCookie(cookie)
	stream := &streamWriter{header: make(http.Header), writes: make(chan string, 20)}
	streamDone := make(chan struct{})
	go func() { app.ServeHTTP(stream, request); close(streamDone) }()
	t.Cleanup(func() { stopStream(); <-streamDone })
	select {
	case ready := <-stream.writes:
		if !strings.Contains(ready, "event: ready") {
			t.Fatalf("ready: %q", ready)
		}
	case <-ctx.Done():
		t.Fatal("stream not ready")
	}
	body := fmt.Sprintf(`{"projectId":%d,"prompt":"test","provider":"codex"}`, project.ID)
	create := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/tasks", strings.NewReader(body))
	create.Header.Set("Origin", "http://127.0.0.1:8080")
	create.Header.Set("X-CSRF-Token", session.CSRFToken)
	create.AddCookie(cookie)
	created := httptest.NewRecorder()
	app.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d", created.Code)
	}
	var previous int64
	for {
		select {
		case chunk := <-stream.writes:
			for _, frame := range strings.Split(chunk, "\n\n") {
				for _, line := range strings.Split(frame, "\n") {
					if !strings.HasPrefix(line, "data: ") {
						continue
					}
					var event store.Event
					if err = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
						t.Fatal(err)
					}
					if event.Sequence <= previous {
						t.Fatalf("event sequence %d after %d", event.Sequence, previous)
					}
					previous = event.Sequence
					if strings.Contains(line, "event-fixture-secret") {
						t.Fatal("secret in SSE")
					}
					if event.Type == "result" {
						if !strings.Contains(line, "[REDACTED]") {
							t.Fatal("result missing redaction")
						}
						return
					}
				}
			}
		case <-ctx.Done():
			t.Fatal("result event not received")
		}
	}
}
