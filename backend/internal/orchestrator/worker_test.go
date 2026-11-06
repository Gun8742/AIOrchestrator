package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
	"pixel-agent-office/backend/internal/testdb"
)

type fakeQueue struct {
	status    string
	block     bool
	result    *string
	errorText *string
}

type failingFinishQueue struct {
	task store.ClaimedTask
	err  error
}

func (f *failingFinishQueue) ClaimNextForProviders(context.Context, []string) (store.ClaimedTask, error) {
	return f.task, nil
}

func (f *failingFinishQueue) FinishTask(context.Context, int64, string, *string, *string, bool) error {
	return f.err
}

func TestWorkerStopsWhenResultCannotBeSaved(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saveErr := errors.New("database unavailable")
	q := &failingFinishQueue{task: store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, err: saveErr}
	w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{Terminated: true}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.Run(ctx); !errors.Is(err, saveErr) {
		t.Fatalf("Run returned %v, want database error", err)
	}
}

func (f *fakeQueue) ClaimNextForProviders(context.Context, []string) (store.ClaimedTask, error) {
	return store.ClaimedTask{}, store.ErrNotFound
}
func (f *fakeQueue) FinishTask(_ context.Context, _ int64, status string, result *string, errorText *string, block bool) error {
	f.status = status
	f.result = result
	f.errorText = errorText
	f.block = block
	return nil
}
func TestUnknownExecutionBlocksProject(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{}, errors.New("disconnected")
	})
	w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if q.status != "failed" || !q.block {
		t.Fatalf("status=%s block=%v", q.status, q.block)
	}
}

func TestUnconfirmedSuccessFailsAndBlocksProject(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{Result: "unconfirmed"}, nil
	})
	err = w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if err != nil || q.status != "failed" || !q.block || q.result != nil {
		t.Fatalf("error=%v status=%s block=%v result=%v", err, q.status, q.block, q.result)
	}
}

func TestMissingCodexDoesNotBlockProject(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, ExecuteCodex(nil, nil))
	err = w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if err != nil || q.status != "failed" || q.block {
		t.Fatalf("error=%v status=%s block=%v", err, q.status, q.block)
	}
}

func TestInitializeFailureDoesNotBlockProject(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nread request\nprintf '%s\\n' '{\"id\":1,\"error\":{\"code\":-1,\"message\":\"initialize failed\"}}'\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, ExecuteCodex(nil, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = w.runTask(ctx, store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if err != nil || q.status != "failed" || q.block {
		t.Fatalf("error=%v status=%s block=%v", err, q.status, q.block)
	}
}
func TestCompletedExecutionDoesNotBlock(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{Result: "done", Terminated: true}, nil
	})
	w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if q.status != "completed" || q.block {
		t.Fatalf("status=%s block=%v", q.status, q.block)
	}
}

func TestCancellationRequiresInterruptedTerminal(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, func(_ context.Context, _ int64, _, _ string, stop <-chan struct{}) (codex.Execution, error) {
		<-stop
		return codex.Execution{Terminated: true}, codex.ErrInterrupted
	})
	ctrl := &control{stop: make(chan struct{})}
	w.active[1] = ctrl
	done := make(chan struct{})
	go func() {
		w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, ctrl)
		close(done)
	}()
	if !w.RequestCancel(1) {
		t.Fatal("cancel unavailable")
	}
	<-done
	if q.status != "cancelled" || q.block {
		t.Fatalf("status=%s block=%v", q.status, q.block)
	}
}

func TestResultRedactsKnownCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "secret-for-test")
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{Result: "token secret-for-test", Terminated: true}, nil
	})
	w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if q.result == nil || *q.result != "token [REDACTED]" {
		t.Fatalf("unexpected sanitized result")
	}
}

func TestKnownRuntimeErrorsProvideSafeGuidance(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cause   error
		message string
	}{
		{ErrApprovalTimeout, "Codex approval timed out; check the task and retry after confirming the project is safe"},
		{codex.ErrUsageLimit, "Codex usage limit reached; check account usage and retry later"},
		{codex.ErrAuthentication, "Codex authentication or account access failed; check CLI sign-in and access"},
		{codex.ErrNetwork, "Codex network or stream failed; check connectivity before retrying"},
		{codex.ErrSandbox, "Codex sandbox failed; check the local runtime configuration"},
	} {
		t.Run(tc.cause.Error(), func(t *testing.T) {
			q := &fakeQueue{}
			w := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
				return codex.Execution{Terminated: true}, tc.cause
			})
			if err := w.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})}); err != nil || q.status != "failed" || q.block || q.errorText == nil || *q.errorText != tc.message {
				t.Fatalf("error=%v status=%s block=%v message=%v", err, q.status, q.block, q.errorText)
			}
		})
	}
}

func TestWorkerRunsDifferentProjectsConcurrentlyAndKeepsProjectFIFO(t *testing.T) {
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
	firstPath, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstProject, err := db.AddProject(ctx, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondProject, err := db.AddProject(ctx, secondPath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateTask(ctx, firstProject.ID, "first", "codex")
	if err != nil {
		t.Fatal(err)
	}
	next, err := db.CreateTask(ctx, firstProject.ID, "next", "codex")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateTask(ctx, secondProject.ID, "other", "codex")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan int64, 3)
	releaseFirst := make(chan struct{})
	releaseOther := make(chan struct{})
	worker := New(db, func(ctx context.Context, id int64, _, _ string, _ <-chan struct{}) (codex.Execution, error) {
		started <- id
		var release <-chan struct{}
		if id == first.ID {
			release = releaseFirst
		} else if id == other.ID {
			release = releaseOther
		}
		if release != nil {
			select {
			case <-release:
			case <-ctx.Done():
				return codex.Execution{}, ctx.Err()
			}
		}
		return codex.Execution{Result: "done", Terminated: true}, nil
	})
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitStarted := func() int64 {
		t.Helper()
		select {
		case id := <-started:
			return id
		case <-ctx.Done():
			t.Fatal("timed out waiting for task")
			return 0
		}
	}
	if id := waitStarted(); id != first.ID {
		t.Fatalf("first started: %d", id)
	}
	if id := waitStarted(); id != other.ID {
		t.Fatalf("different project did not start concurrently: %d", id)
	}
	snapshot, err := db.Task(ctx, next.ID)
	if err != nil || snapshot.Task.Status != "queued" {
		t.Fatalf("second task while first active: %#v %v", snapshot.Task, err)
	}
	close(releaseFirst)
	if id := waitStarted(); id != next.ID {
		t.Fatalf("next FIFO task: %d", id)
	}
	snapshot, err = db.Task(ctx, other.ID)
	if err != nil || snapshot.Task.Status != "running" {
		t.Fatalf("other project should remain active: %#v %v", snapshot.Task, err)
	}
	close(releaseOther)
}

func TestCancellationDuringCodexInitialization(t *testing.T) {
	dir, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nIFS= read -r request\ndir=${0%/*}\nprintf started > \"$dir/started\"\nIFS= read -r request\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	q := &fakeQueue{}
	worker := New(q, ExecuteCodex(nil, nil))
	ctrl := &control{stop: make(chan struct{})}
	worker.active[1] = ctrl
	done := make(chan error, 1)
	go func() {
		done <- worker.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: dir}, ctrl)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			worker.RequestCancel(1)
			t.Fatal("initialization did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !worker.RequestCancel(1) {
		t.Fatal("cancel unavailable")
	}
	select {
	case err := <-done:
		if err != nil || q.status != "cancelled" || q.block {
			t.Fatalf("error=%v status=%s block=%v", err, q.status, q.block)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("initialization did not stop")
	}
}

func TestApprovalTimeoutWithoutTerminalConfirmationBlocksProject(t *testing.T) {
	path, err := store.CanonicalDirectory(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	q := &fakeQueue{}
	worker := New(q, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
		return codex.Execution{}, ErrApprovalTimeout
	})
	err = worker.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1}, ProjectPath: path}, &control{stop: make(chan struct{})})
	if err != nil || q.status != "failed" || !q.block || q.errorText == nil || !strings.Contains(*q.errorText, "approval timed out") {
		t.Fatalf("error=%v status=%s blocked=%v message=%v", err, q.status, q.block, q.errorText)
	}
}

func TestProviderSchedulingPreservesFIFOWithoutFallback(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := store.Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	firstPath, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	otherPath, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, firstPath)
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := db.AddProject(ctx, otherPath)
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.CreateTask(ctx, project.ID, "first", "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	next, err := db.CreateTask(ctx, project.ID, "next", "codex")
	if err != nil {
		t.Fatal(err)
	}
	other, err := db.CreateTask(ctx, otherProject.ID, "other", "codex")
	if err != nil {
		t.Fatal(err)
	}
	type observed struct {
		provider string
		id       int64
	}
	started := make(chan observed, 3)
	execute := func(provider string) Execute {
		return func(_ context.Context, id int64, _, _ string, _ <-chan struct{}) (codex.Execution, error) {
			started <- observed{provider, id}
			return codex.Execution{Result: "done", Terminated: true}, nil
		}
	}
	firstCtx, stopFirst := context.WithCancel(ctx)
	firstWorker := New(db, execute("codex"))
	firstDone := make(chan error, 1)
	go func() { firstDone <- firstWorker.Run(firstCtx) }()
	t.Cleanup(func() {
		stopFirst()
		if firstDone != nil {
			<-firstDone
		}
	})
	wait := func() observed {
		t.Helper()
		select {
		case result := <-started:
			return result
		case <-ctx.Done():
			t.Fatal("task did not start")
			return observed{}
		}
	}
	if got := wait(); got != (observed{"codex", other.ID}) {
		t.Fatalf("unavailable provider fell back or blocked other project: %#v", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, err := db.Task(ctx, other.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Task.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("other task did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, id := range []int64{first.ID, next.ID} {
		snapshot, err := db.Task(ctx, id)
		if err != nil || snapshot.Task.Status != "queued" {
			t.Fatalf("task %d should remain queued: %#v %v", id, snapshot.Task, err)
		}
	}
	stopFirst()
	<-firstDone
	firstDone = nil
	secondCtx, stopSecond := context.WithCancel(ctx)
	secondWorker := NewWithAntigravity(db, execute("codex"), execute("antigravity"))
	secondDone := make(chan error, 1)
	go func() { secondDone <- secondWorker.Run(secondCtx) }()
	t.Cleanup(func() { stopSecond(); <-secondDone })
	if got := wait(); got != (observed{"antigravity", first.ID}) {
		t.Fatalf("first provider: %#v", got)
	}
	if got := wait(); got != (observed{"codex", next.ID}) {
		t.Fatalf("FIFO handoff: %#v", got)
	}
}

func TestMixedProviderWorkflowHandoff(t *testing.T) {
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
	steps := []store.WorkflowStepInput{{Provider: "codex", Prompt: "first"}, {Provider: "antigravity", Prompt: "second"}, {Provider: "codex", Prompt: "third"}}
	workflow, err := db.CreateWorkflow(ctx, project.ID, steps)
	if err != nil {
		t.Fatal(err)
	}
	type call struct{ provider, prompt string }
	calls := make(chan call, 3)
	codexRunner := func(_ context.Context, _ int64, _, prompt string, _ <-chan struct{}) (codex.Execution, error) {
		calls <- call{"codex", prompt}
		if prompt == "first" {
			return codex.Execution{Terminated: true, Result: "first result"}, nil
		}
		return codex.Execution{Terminated: true, Result: "final result"}, nil
	}
	agyRunner := func(_ context.Context, _ int64, _, prompt string, _ <-chan struct{}) (codex.Execution, error) {
		calls <- call{"antigravity", prompt}
		return codex.Execution{Terminated: true, Result: "second result"}, nil
	}
	worker := NewWithAntigravity(db, codexRunner, agyRunner)
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	var recorded []call
	for len(recorded) < 3 {
		select {
		case next := <-calls:
			recorded = append(recorded, next)
		case <-ctx.Done():
			t.Fatal("workflow did not finish")
		}
	}
	if recorded[0] != (call{"codex", "first"}) || recorded[1].provider != "antigravity" || !strings.Contains(recorded[1].prompt, "first result") || recorded[2].provider != "codex" || !strings.Contains(recorded[2].prompt, "second result") {
		t.Fatalf("unexpected handoff: %#v", recorded)
	}
	for {
		snapshot, err := db.Workflow(ctx, workflow.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Status == "completed" {
			if len(snapshot.Steps) != 3 || snapshot.Steps[2].Result == nil || *snapshot.Steps[2].Result != "final result" {
				t.Fatalf("bad workflow result: %#v", snapshot)
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("workflow did not become completed")
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func TestAntigravityCleanupFollowsSuccessfulTerminalCommit(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		q := &fakeQueue{}
		w := NewWithAntigravity(q, nil, nil)
		called := false
		w.SetAntigravityCleanup(func(context.Context, int64) error {
			if q.status != "completed" {
				t.Fatal("cleanup preceded terminal persistence")
			}
			called = true
			return errors.New("cleanup unavailable")
		})
		if err := w.finishTask(store.Task{ID: 1, Provider: "antigravity"}, "completed", nil, nil, blocked); err != nil {
			t.Fatal(err)
		}
		if called == blocked {
			t.Fatal("cleanup did not respect execution confirmation")
		}
	}
	q := &failingFinishQueue{err: errors.New("commit failed")}
	w := NewWithAntigravity(q, nil, nil)
	w.SetAntigravityCleanup(func(context.Context, int64) error { t.Fatal("cleanup ran after failed commit"); return nil })
	if err := w.finishTask(store.Task{ID: 1, Provider: "antigravity"}, "completed", nil, nil, false); err == nil {
		t.Fatal("commit failure lost")
	}
}
