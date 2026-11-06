package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"pixel-agent-office/backend/internal/testdb"
	"testing"
	"time"
)

func TestCanonicalDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "project")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	actual, err := CanonicalDirectory(link)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("got %q, want %q", actual, expected)
	}
	for _, path := range []string{"relative", filepath.Join(root, "missing")} {
		if _, err := CanonicalDirectory(path); err == nil {
			t.Errorf("expected error for %q", path)
		}
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CanonicalDirectory(file); err == nil {
		t.Error("expected error for file")
	}
}

func TestProjectPersistence(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.Pool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", project.ID) })
	if _, err := db.AddProject(ctx, path); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	projects, err := db.Projects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range projects {
		if p.ID == project.ID && p.Path == path {
			found = true
		}
	}
	if !found {
		t.Fatal("project missing after insert")
	}
}

func TestTaskSnapshotAndFIFO(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.Pool.Exec(ctx, `DELETE FROM approvals WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM events WHERE task_id IN (SELECT id FROM tasks WHERE project_id=$1)`, project.ID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM tasks WHERE project_id=$1`, project.ID)
		_, _ = db.Pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, project.ID)
	})
	first, err := db.CreateTask(ctx, project.ID, "first", "codex")
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateTask(ctx, project.ID, "second", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if first.QueueOrder >= second.QueueOrder {
		t.Fatal("FIFO order is not preserved")
	}
	snapshot, err := db.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Sequence < second.QueueOrder {
		t.Fatal("snapshot watermark omits task")
	}
	found := false
	for _, task := range snapshot.Tasks {
		if task.ID == second.ID && task.Status == "queued" {
			found = true
		}
	}
	if !found {
		t.Fatal("task absent from snapshot")
	}
	detail, err := db.Task(ctx, first.ID)
	if err != nil || len(detail.Events) != 1 || detail.Events[0].Payload == nil {
		t.Fatalf("detail: %#v %v", detail, err)
	}
	cancelled, err := db.CancelQueued(ctx, first.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("cancel: %#v %v", cancelled, err)
	}
	repeated, err := db.CancelQueued(ctx, first.ID)
	if err != nil || repeated.Status != "cancelled" {
		t.Fatalf("repeat cancel: %#v %v", repeated, err)
	}
	detail, err = db.Task(ctx, first.ID)
	if err != nil || len(detail.Events) != 2 || detail.Sequence < detail.Events[1].Sequence {
		t.Fatalf("cancel detail: %#v %v", detail, err)
	}
	events, err := db.EventsAfter(ctx, first.QueueOrder)
	if err != nil || len(events) < 2 {
		t.Fatalf("event stream: %#v %v", events, err)
	}
	db.boot = bootIdentity{host: "test-host", boot: "first-boot"}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != second.ID || claimed.Task.Status != "running" {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	approval, err := db.BeginApproval(ctx, second.ID, "command", json.RawMessage(`{"command":"example"}`))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := db.Task(ctx, second.ID)
	if err != nil || pending.PendingApproval == nil || pending.PendingApproval.ID != approval.ID || pending.Task.Status != "waiting_for_user" {
		t.Fatalf("pending approval: %#v %v", pending, err)
	}
	if err := db.ResolveApproval(ctx, second.ID, approval.ID, "deny"); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolveApproval(ctx, second.ID, approval.ID, "approve"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate approval: %v", err)
	}
	pending, err = db.Task(ctx, second.ID)
	if err != nil || pending.PendingApproval != nil || pending.Task.Status != "running" {
		t.Fatalf("resolved approval: %#v %v", pending, err)
	}
	third, err := db.CreateTask(ctx, project.ID, "third", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("parallel claim: %v", err)
	}
	if err := db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blocked project claim: %v", err)
	}
	detail, err = db.Task(ctx, second.ID)
	if err != nil || detail.Task.Status != "failed" {
		t.Fatalf("recovery: %#v %v", detail.Task, err)
	}
	detail, err = db.Task(ctx, third.ID)
	if err != nil || detail.Task.Status != "queued" {
		t.Fatalf("queue after recovery: %#v %v", detail.Task, err)
	}
	db.boot = bootIdentity{host: "other-host", boot: "second-boot"}
	if err = db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong host must remain blocked: %v", err)
	}
	db.boot = bootIdentity{host: "test-host", boot: "second-boot"}
	if err = db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	resumed, err := db.ClaimNext(ctx)
	if err != nil || resumed.Task.ID != third.ID {
		t.Fatalf("post-reboot FIFO: %#v %v", resumed, err)
	}

	if _, err := db.CreateTask(ctx, -1, "missing", "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: %v", err)
	}
}

func TestConcurrentSnapshotsAndClaims(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	const count = 12
	start := make(chan struct{})
	outcomes := make(chan error, count)
	for i := 0; i < count; i++ {
		go func(index int) {
			<-start
			_, err := db.CreateTask(ctx, project.ID, fmt.Sprintf("task %d", index), "codex")
			outcomes <- err
		}(i)
	}
	close(start)
	for i := 0; i < count; i++ {
		snapshot, err := db.Tasks(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Sequence != int64(len(snapshot.Tasks)) {
			t.Fatalf("snapshot watermark=%d tasks=%d", snapshot.Sequence, len(snapshot.Tasks))
		}
		for _, task := range snapshot.Tasks {
			if task.QueueOrder > snapshot.Sequence {
				t.Fatal("task newer than snapshot watermark")
			}
		}
		if err := <-outcomes; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := db.Tasks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Tasks) != count || snapshot.Sequence != count {
		t.Fatalf("final snapshot=%d tasks=%d", snapshot.Sequence, len(snapshot.Tasks))
	}
	claims := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := db.ClaimNext(ctx); claims <- err }()
	}
	successes, empty := 0, 0
	for i := 0; i < 2; i++ {
		err := <-claims
		if err == nil {
			successes++
		} else if errors.Is(err, ErrNotFound) {
			empty++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || empty != 1 {
		t.Fatalf("concurrent claims: successes=%d empty=%d", successes, empty)
	}
}

func TestProviderMigrationPreservesExistingTask(t *testing.T) {
	databaseURL := testdb.URL(t)
	ctx := context.Background()
	db, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	_, err = db.Pool.Exec(ctx, `CREATE TABLE projects (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,name TEXT NOT NULL,path TEXT NOT NULL UNIQUE,queue_state TEXT NOT NULL DEFAULT 'ready',blocked_reason TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE tasks (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,project_id BIGINT NOT NULL REFERENCES projects(id),prompt TEXT NOT NULL,status TEXT NOT NULL,queue_order BIGINT UNIQUE NOT NULL,result TEXT,error TEXT,created_at TIMESTAMPTZ NOT NULL DEFAULT now(),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
 INSERT INTO projects(name,path) VALUES('old','/old'); INSERT INTO tasks(project_id,prompt,status,queue_order) VALUES(1,'old task','completed',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err := db.Task(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Task.Provider != "codex" || detail.Task.Prompt != "old task" || detail.Task.Status != "completed" {
		t.Fatalf("old task not preserved: %#v", detail.Task)
	}
}

func TestTerminalOutcomeEventsAreStoredWithTask(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ status, kind, message string }{{"completed", "result", "finished"}, {"failed", "error", "safe error"}} {
		task, err := db.CreateTask(ctx, project.ID, "work", "codex")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ClaimNext(ctx); err != nil {
			t.Fatal(err)
		}
		var result, failure *string
		if tc.status == "completed" {
			result = &tc.message
		} else {
			failure = &tc.message
		}
		if err = db.FinishTask(ctx, task.ID, tc.status, result, failure, false); err != nil {
			t.Fatal(err)
		}
		detail, err := db.Task(ctx, task.ID)
		if err != nil {
			t.Fatal(err)
		}
		last := detail.Events[len(detail.Events)-1]
		var payload map[string]string
		if err = json.Unmarshal(last.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if detail.Task.Status != tc.status || last.Type != tc.kind || payload[tc.kind] != tc.message || last.Sequence > detail.Sequence {
			t.Fatalf("outcome: %#v event: %#v", detail.Task, last)
		}
	}
}

func TestMigrationPreservesV1TasksAndQueueOrder(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	_, err = db.Pool.Exec(ctx, `
CREATE TABLE projects (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, name TEXT NOT NULL, path TEXT NOT NULL UNIQUE, queue_state TEXT NOT NULL DEFAULT 'ready', blocked_reason TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE event_counter (id INTEGER PRIMARY KEY, value BIGINT NOT NULL);
INSERT INTO event_counter(id,value) VALUES (1,1);
CREATE TABLE tasks (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id), prompt TEXT NOT NULL, status TEXT NOT NULL, queue_order BIGINT UNIQUE NOT NULL, result TEXT, error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE events (sequence BIGINT PRIMARY KEY, task_id BIGINT NOT NULL REFERENCES tasks(id), type TEXT NOT NULL, payload JSONB NOT NULL, timestamp TIMESTAMPTZ NOT NULL DEFAULT now());
INSERT INTO projects(name,path) VALUES ('legacy','/legacy');
INSERT INTO tasks(project_id,prompt,status,queue_order) VALUES (1,'legacy prompt','queued',1);
INSERT INTO events(sequence,task_id,type,payload) VALUES (1,1,'status','{"status":"queued"}');`)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err := db.Task(ctx, 1)
	if err != nil || detail.Task.Provider != "codex" || detail.Task.Prompt != "legacy prompt" || detail.Task.Status != "queued" || len(detail.Events) != 1 || detail.Events[0].Sequence != 1 {
		t.Fatalf("legacy task: %#v %v", detail, err)
	}
	created, err := db.CreateTask(ctx, 1, "new prompt", "codex")
	if err != nil || created.QueueOrder <= detail.Task.QueueOrder {
		t.Fatalf("new task: %#v %v", created, err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != detail.Task.ID {
		t.Fatalf("FIFO after migration: %#v %v", claimed, err)
	}
}

func TestBootChangeDoesNotUnblockOtherProvider(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	db.boot = bootIdentity{host: "test-host", boot: "first-boot"}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(ctx, project.ID, "test", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE tasks SET provider='antigravity' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	message := "unconfirmed execution"
	if err = db.FinishTask(ctx, task.ID, "failed", nil, &message, true); err != nil {
		t.Fatal(err)
	}
	queued, err := db.CreateTask(ctx, project.ID, "queued", "codex")
	if err != nil {
		t.Fatal(err)
	}
	db.boot = bootIdentity{host: "test-host", boot: "second-boot"}
	if err = db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-Codex execution unexpectedly unblocked: %v", err)
	}
	snapshot, err := db.Task(ctx, queued.ID)
	if err != nil || snapshot.Task.Status != "queued" {
		t.Fatalf("queued task: %#v %v", snapshot.Task, err)
	}
}

func TestOutcomeFailureRollsBackTaskEventsAndWorkflow(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	path, err := CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project, err := db.AddProject(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "one"}, {Provider: "codex", Prompt: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := db.CurrentSequence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Pool.Exec(ctx, `CREATE FUNCTION reject_fixture_outcome() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic event persistence failure'; END $$;
 CREATE TRIGGER reject_fixture_outcome BEFORE INSERT ON events FOR EACH ROW WHEN (NEW.type = 'result') EXECUTE FUNCTION reject_fixture_outcome()`)
	if err != nil {
		t.Fatal(err)
	}
	result := "fixture result"
	if err = db.FinishTask(ctx, claimed.Task.ID, "completed", &result, nil, false); err == nil {
		t.Fatal("expected event persistence failure")
	}
	task, err := db.Task(ctx, claimed.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Task.Status != "running" || task.Task.Result != nil || task.Sequence != before {
		t.Fatal("task or watermark escaped rollback")
	}
	events, err := db.EventsAfter(ctx, before)
	if err != nil || len(events) != 0 {
		t.Fatal("rolled-back events became observable")
	}
	snapshot, err := db.Workflow(ctx, workflow.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "running" || snapshot.Steps[1].TaskID != nil {
		t.Fatal("workflow advanced despite failed terminal commit")
	}
	if _, err = db.Pool.Exec(ctx, `DROP TRIGGER reject_fixture_outcome ON events`); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishTask(ctx, claimed.Task.ID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	events, err = db.EventsAfter(ctx, before)
	if err != nil || len(events) == 0 || events[0].Sequence != before+1 {
		t.Fatal("counter rollback was not preserved")
	}
	snapshot, err = db.Workflow(ctx, workflow.ID)
	if err != nil || snapshot.Steps[1].TaskID == nil {
		t.Fatal("workflow did not advance after successful terminal commit")
	}
}

func TestCanonicalDirectoryRequiresReadAndSearchPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory access permissions")
	}
	path := t.TempDir()
	t.Cleanup(func() { _ = os.Chmod(path, 0700) })
	for _, mode := range []os.FileMode{0000, 0400, 0100} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := CanonicalDirectory(path); err == nil {
			t.Errorf("accepted directory without read/search permission: %04o", mode)
		}
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
}
