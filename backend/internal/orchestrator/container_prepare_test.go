package orchestrator

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/store"
	"pixel-agent-office/backend/internal/testdb"
)

func TestPrepareContainerPersistsReservationBeforeCreate(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "creation uncertain"
		}
		t.Run(name, func(t *testing.T) {
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
			id := strings.Repeat("a", 64)
			create := func(_ context.Context, owner, daemon, image, projectPath string) (antigravity.ContainerRef, error) {
				saved, err := db.ContainerExecution(ctx, task.ID)
				if err != nil || saved.OwnerToken != owner || saved.DaemonID != daemon || saved.ContainerID != nil || projectPath != path {
					t.Fatalf("reservation missing before create: %#v %v", saved, err)
				}
				if fail {
					return antigravity.ContainerRef{}, antigravity.ErrContainerCreate
				}
				return antigravity.ContainerRef{ID: id, Owner: owner, DaemonID: daemon}, nil
			}
			ref, err := prepareContainer(ctx, db, task.ID, "daemon", "fixture-image", path, create)
			if fail {
				if !errors.Is(err, antigravity.ErrContainerCreate) {
					t.Fatalf("unexpected error: %v", err)
				}
				if err = db.FinishTask(ctx, task.ID, "failed", nil, nil, true); err != nil {
					t.Fatal(err)
				}
				blocked, err := db.BlockedContainerExecutions(ctx)
				if err != nil || len(blocked) != 1 || blocked[0].ContainerID != nil {
					t.Fatalf("uncertain creation not retained: %#v %v", blocked, err)
				}
				return
			}
			if err != nil || ref.ID != id {
				t.Fatalf("prepare: %#v %v", ref, err)
			}
			saved, err := db.ContainerExecution(ctx, task.ID)
			if err != nil || saved.ContainerID == nil || *saved.ContainerID != id {
				t.Fatalf("container identity not saved: %#v %v", saved, err)
			}
		})
	}
}

func TestDockerPreparePersistsIdentity(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	t.Cleanup(func() {
		execution, err := db.ContainerExecution(context.Background(), task.ID)
		if err == nil {
			_ = exec.Command("docker", "rm", "-f", "pixel-agent-"+execution.OwnerToken).Run()
		}
	})
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot identify Docker daemon")
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", "pixel-agent-office-agy:1.4.9").Output()
	if err != nil {
		t.Fatal("build Antigravity test image first")
	}
	ref, err := PrepareContainer(ctx, db, task.ID, strings.TrimSpace(string(daemon)), strings.TrimSpace(string(image)), path)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || execution.ContainerID == nil || *execution.ContainerID != ref.ID || execution.OwnerToken != ref.Owner {
		t.Fatalf("identity: %#v %v", execution, err)
	}
	state, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Status}}", ref.ID).Output()
	if err != nil || strings.TrimSpace(string(state)) != "created" {
		t.Fatal("container was not safely created")
	}
}
