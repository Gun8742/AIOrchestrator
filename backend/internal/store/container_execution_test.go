package store

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pixel-agent-office/backend/internal/testdb"
)

func TestContainerExecutionIdentityIsDurableAndWriteOnce(t *testing.T) {
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
	task, err := db.CreateTask(ctx, project.ID, "fixture", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	owner, other, container := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 64)
	if err = db.ReserveContainerExecution(ctx, task.ID, owner, "daemon-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Codex reservation: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE tasks SET provider='antigravity' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if err = db.ReserveContainerExecution(ctx, task.ID, owner, "daemon-1"); err != nil {
		t.Fatal(err)
	}
	reserved, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || reserved.OwnerToken != owner || reserved.DaemonID != "daemon-1" || reserved.ContainerID != nil {
		t.Fatalf("reservation: %#v %v", reserved, err)
	}
	if err = db.ReserveContainerExecution(ctx, task.ID, other, "daemon-2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate reservation: %v", err)
	}
	if err = db.AttachContainerID(ctx, task.ID, other, container); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong owner: %v", err)
	}
	if err = db.AttachContainerID(ctx, task.ID, owner, container); err != nil {
		t.Fatal(err)
	}
	if err = db.AttachContainerID(ctx, task.ID, owner, strings.Repeat("d", 64)); !errors.Is(err, ErrConflict) {
		t.Fatalf("replaced container: %v", err)
	}
	if err = db.FinishTask(ctx, task.ID, "failed", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	saved, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || saved.ContainerID == nil || *saved.ContainerID != container || saved.OwnerToken != owner {
		t.Fatalf("saved identity: %#v %v", saved, err)
	}
	if err = db.AttachContainerID(ctx, task.ID, owner, container); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal task mutation: %v", err)
	}
}

func TestReleaseContainerBlockChecksRecordedExecution(t *testing.T) {
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
	task, err := db.CreateTask(ctx, project.ID, "fixture", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimNext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE tasks SET provider='antigravity' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	owner, container := strings.Repeat("a", 32), strings.Repeat("c", 64)
	if err = db.ReserveContainerExecution(ctx, task.ID, owner, "daemon-1"); err != nil {
		t.Fatal(err)
	}
	if err = db.FinishTask(ctx, task.ID, "failed", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	queued, err := db.CreateTask(ctx, project.ID, "next", "codex")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := db.BlockedContainerExecutions(ctx)
	if err != nil || len(blocked) != 1 || blocked[0].ContainerID != nil {
		t.Fatalf("blocked executions: %#v %v", blocked, err)
	}
	wrong := blocked[0]
	wrong.DaemonID = "wrong-daemon"
	if err = db.ReleaseContainerBlock(ctx, wrong, container); !errors.Is(err, ErrConflict) {
		t.Fatalf("wrong daemon: %v", err)
	}
	if _, err = db.ClaimNext(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unverified block released: %v", err)
	}
	if err = db.ReleaseContainerBlock(ctx, blocked[0], container); err != nil {
		t.Fatal(err)
	}
	if err = db.ReleaseContainerBlock(ctx, blocked[0], container); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate release: %v", err)
	}
	execution, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || execution.ContainerID == nil || *execution.ContainerID != container {
		t.Fatalf("recovered identity: %#v %v", execution, err)
	}
	detail, err := db.Task(ctx, task.ID)
	if err != nil || len(detail.Events) == 0 || detail.Events[len(detail.Events)-1].Type != "project" {
		t.Fatalf("missing project event: %#v %v", detail.Events, err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != queued.ID {
		t.Fatalf("queue not resumed: %#v %v", claimed, err)
	}
}
