package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pixel-agent-office/backend/internal/testdb"
)

func TestCreateWorkflowQueuesFirstStepAtomically(t *testing.T) {
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
	first, err := db.CreateTask(ctx, project.ID, "ordinary", "codex")
	if err != nil {
		t.Fatal(err)
	}
	created, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "first step"}, {Provider: "codex", Prompt: "second step"}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Task(ctx, created.FirstTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Task.Status != "queued" || snapshot.Task.Prompt != "first step" || snapshot.Task.QueueOrder <= first.QueueOrder || len(snapshot.Events) != 1 {
		t.Fatalf("unexpected first step: %#v", snapshot)
	}
	var steps, linked int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*), count(task_id) FROM workflow_steps WHERE workflow_id=$1`, created.ID).Scan(&steps, &linked); err != nil {
		t.Fatal(err)
	}
	if steps != 2 || linked != 1 {
		t.Fatalf("steps=%d linked=%d", steps, linked)
	}
	if _, err = db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "ok"}, {Provider: "unknown", Prompt: "no"}}); !errors.Is(err, ErrInvalidWorkflow) {
		t.Fatalf("invalid workflow: %v", err)
	}
	var count int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM workflows`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("unexpected workflow count %d", count)
	}
}

func TestWorkflowHandoffUsesFIFOAndStopsOnFailure(t *testing.T) {
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
	workflow, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "step one"}, {Provider: "codex", Prompt: "step two"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := db.ClaimNext(ctx)
	if err != nil || first.Task.ID != workflow.FirstTaskID {
		t.Fatalf("first claim: %#v %v", first, err)
	}
	ordinary, err := db.CreateTask(ctx, project.ID, "ordinary", "codex")
	if err != nil {
		t.Fatal(err)
	}
	result := "handoff result"
	if err = db.FinishTask(ctx, first.Task.ID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	workflowSnapshot, err := db.Workflow(ctx, workflow.ID)
	if err != nil || len(workflowSnapshot.Steps) != 2 || workflowSnapshot.Steps[0].Result == nil || *workflowSnapshot.Steps[0].Result != result || workflowSnapshot.Steps[1].TaskID == nil || workflowSnapshot.Sequence <= first.Task.QueueOrder {
		t.Fatalf("workflow snapshot: %#v %v", workflowSnapshot, err)
	}
	var nextID int64
	if err = db.Pool.QueryRow(ctx, `SELECT task_id FROM workflow_steps WHERE workflow_id=$1 AND step_index=1`, workflow.ID).Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	next, err := db.Task(ctx, nextID)
	if err != nil || !strings.Contains(next.Task.Prompt, result) {
		t.Fatalf("handoff: %#v %v", next, err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != ordinary.ID {
		t.Fatalf("FIFO: %#v %v", claimed, err)
	}
	if err = db.FinishTask(ctx, ordinary.ID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	claimed, err = db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != nextID {
		t.Fatalf("next claim: %#v %v", claimed, err)
	}
	message := "failure"
	if err = db.FinishTask(ctx, nextID, "failed", nil, &message, false); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.Pool.QueryRow(ctx, `SELECT status FROM workflows WHERE id=$1`, workflow.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("workflow status=%s", status)
	}
}

func TestWorkflowCancelBeforeCompletionPreventsHandoff(t *testing.T) {
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
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE workflows SET cancel_requested=true WHERE id=$1`, workflow.ID); err != nil {
		t.Fatal(err)
	}
	result := "done"
	if err = db.FinishTask(ctx, workflow.FirstTaskID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	var status string
	var linked int
	if err = db.Pool.QueryRow(ctx, `SELECT w.status,count(s.task_id) FROM workflows w JOIN workflow_steps s ON s.workflow_id=w.id WHERE w.id=$1 GROUP BY w.status`, workflow.ID).Scan(&status, &linked); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || linked != 1 {
		t.Fatalf("status=%s linked=%d", status, linked)
	}
	uncertain, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "one"}, {Provider: "codex", Prompt: "two"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE workflows SET cancel_requested=true WHERE id=$1`, uncertain.ID); err != nil {
		t.Fatal(err)
	}
	message := "termination uncertain"
	if err = db.FinishTask(ctx, uncertain.FirstTaskID, "failed", nil, &message, true); err != nil {
		t.Fatal(err)
	}
	if err = db.Pool.QueryRow(ctx, `SELECT status FROM workflows WHERE id=$1`, uncertain.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("uncertain termination status=%s", status)
	}
}

func TestCancelWorkflowQueuedAndActive(t *testing.T) {
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
	steps := []WorkflowStepInput{{Provider: "codex", Prompt: "one"}, {Provider: "codex", Prompt: "two"}}
	queued, err := db.CreateWorkflow(ctx, project.ID, steps)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := db.CancelWorkflow(ctx, queued.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.ActiveTaskID != nil {
		t.Fatalf("queued cancel=%#v %v", cancelled, err)
	}
	detail, err := db.Task(ctx, queued.FirstTaskID)
	if err != nil || detail.Task.Status != "cancelled" {
		t.Fatalf("task=%#v %v", detail, err)
	}
	active, err := db.CreateWorkflow(ctx, project.ID, steps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := db.CancelWorkflow(ctx, active.ID)
	if err != nil || pending.Status != "running" || pending.ActiveTaskID == nil || *pending.ActiveTaskID != active.FirstTaskID {
		t.Fatalf("active cancel=%#v %v", pending, err)
	}
	result := "done"
	if err = db.FinishTask(ctx, active.FirstTaskID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	cancelled, err = db.CancelWorkflow(ctx, active.ID)
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("finished cancel=%#v %v", cancelled, err)
	}
}

func TestCancelQueuedTaskUpdatesWorkflow(t *testing.T) {
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
	if _, err = db.CancelQueued(ctx, workflow.FirstTaskID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = db.Pool.QueryRow(ctx, `SELECT status FROM workflows WHERE id=$1`, workflow.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" {
		t.Fatalf("workflow status=%s", status)
	}
}

func TestWorkflowCancelRacesCompletion(t *testing.T) {
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
	for i := 0; i < 5; i++ {
		workflow, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "one"}, {Provider: "codex", Prompt: "two"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ClaimNext(ctx); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() { <-start; _, e := db.CancelWorkflow(ctx, workflow.ID); done <- e }()
		go func() {
			<-start
			result := "done"
			done <- db.FinishTask(ctx, workflow.FirstTaskID, "completed", &result, nil, false)
		}()
		close(start)
		for j := 0; j < 2; j++ {
			if e := <-done; e != nil {
				t.Fatal(e)
			}
		}
		var status string
		var queued int
		if err = db.Pool.QueryRow(ctx, `SELECT w.status,count(*) FILTER (WHERE t.status='queued') FROM workflows w JOIN workflow_steps s ON s.workflow_id=w.id LEFT JOIN tasks t ON t.id=s.task_id WHERE w.id=$1 GROUP BY w.status`, workflow.ID).Scan(&status, &queued); err != nil {
			t.Fatal(err)
		}
		if status != "cancelled" || queued != 0 {
			t.Fatalf("status=%s queued=%d", status, queued)
		}
	}
}

func TestWorkflowRecoveryStopsInterruptedStepAndPreservesQueuedWork(t *testing.T) {
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
	steps := []WorkflowStepInput{{Provider: "codex", Prompt: "first"}, {Provider: "codex", Prompt: "second"}}
	interrupted, err := db.CreateWorkflow(ctx, project.ID, steps)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := db.CreateWorkflow(ctx, project.ID, steps)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := db.ClaimNext(ctx)
	if err != nil || claim.Task.ID != interrupted.FirstTaskID {
		t.Fatalf("claim: %#v %v", claim, err)
	}
	if err = db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Workflow(ctx, interrupted.ID)
	if err != nil || snapshot.Status != "failed" || len(snapshot.Steps) != 2 || snapshot.Steps[0].Status == nil || *snapshot.Steps[0].Status != "failed" || snapshot.Steps[1].TaskID != nil {
		t.Fatalf("interrupted workflow: %#v %v", snapshot, err)
	}
	if _, err = db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("project must remain blocked: %v", err)
	}
	db.boot = bootIdentity{host: "test-host", boot: "second-boot"}
	if err = db.RecoverInterrupted(ctx); err != nil {
		t.Fatal(err)
	}
	claim, err = db.ClaimNext(ctx)
	if err != nil || claim.Task.ID != queued.FirstTaskID {
		t.Fatalf("queued workflow after recovery: %#v %v", claim, err)
	}
}

func TestWorkflowRejectsOversizedHandoffWithoutBlockingQueue(t *testing.T) {
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
	workflow, err := db.CreateWorkflow(ctx, project.ID, []WorkflowStepInput{{Provider: "codex", Prompt: "first"}, {Provider: "codex", Prompt: "second"}})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := db.CreateTask(ctx, project.ID, "next independent task", "codex")
	if err != nil {
		t.Fatal(err)
	}
	claim, err := db.ClaimNext(ctx)
	if err != nil || claim.Task.ID != workflow.FirstTaskID {
		t.Fatalf("first claim: %#v %v", claim, err)
	}
	result := strings.Repeat("ก", 4000)
	if err = db.FinishTask(ctx, claim.Task.ID, "completed", &result, nil, false); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Workflow(ctx, workflow.ID)
	if err != nil || snapshot.Status != "failed" || snapshot.Error == nil || !strings.Contains(*snapshot.Error, "exceeds") || len(snapshot.Steps) != 2 || snapshot.Steps[1].TaskID != nil {
		t.Fatalf("workflow: %#v %v", snapshot, err)
	}
	claim, err = db.ClaimNext(ctx)
	if err != nil || claim.Task.ID != queued.ID {
		t.Fatalf("independent task: %#v %v", claim, err)
	}
}
