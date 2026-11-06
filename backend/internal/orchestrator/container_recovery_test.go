package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

func TestContainerRecoveryOnlyReleasesVerifiedExecution(t *testing.T) {
	for _, tc := range []struct {
		name                                    string
		attached, mismatch, stopFails, released bool
	}{
		{"recover recorded ID", true, false, false, true},
		{"recover ID from label", false, false, false, true},
		{"unconfirmed stop stays blocked", true, false, true, false},
		{"mismatched discovery stays blocked", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
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
			owner, id := strings.Repeat("a", 32), strings.Repeat("b", 64)
			if err = db.ReserveContainerExecution(ctx, task.ID, owner, "daemon"); err != nil {
				t.Fatal(err)
			}
			if tc.attached {
				if err = db.AttachContainerID(ctx, task.ID, owner, id); err != nil {
					t.Fatal(err)
				}
			}
			if err = db.FinishTask(ctx, task.ID, "failed", nil, nil, true); err != nil {
				t.Fatal(err)
			}
			queued, err := db.CreateTask(ctx, project.ID, "next", "codex")
			if err != nil {
				t.Fatal(err)
			}
			stops := 0
			find := func(_ context.Context, owner, daemon string) (antigravity.ContainerRef, error) {
				if tc.mismatch {
					owner = strings.Repeat("c", 32)
				}
				return antigravity.ContainerRef{ID: id, Owner: owner, DaemonID: daemon}, nil
			}
			stop := func(_ context.Context, ref antigravity.ContainerRef) error {
				stops++
				if ref.ID != id || ref.Owner != owner || ref.DaemonID != "daemon" {
					t.Fatalf("wrong target: %#v", ref)
				}
				if tc.stopFails {
					return antigravity.ErrContainerUnconfirmed
				}
				return nil
			}
			if err = recoverContainers(ctx, db, find, stop); err != nil {
				t.Fatal(err)
			}
			claimed, err := db.ClaimNext(ctx)
			if tc.released {
				if err != nil || claimed.Task.ID != queued.ID || stops != 1 {
					t.Fatalf("queue not resumed: %#v %v stops=%d", claimed, err, stops)
				}
			} else if !errors.Is(err, store.ErrNotFound) || (tc.mismatch && stops != 0) {
				t.Fatalf("unsafe release: %#v %v stops=%d", claimed, err, stops)
			}
		})
	}
}

func TestDockerRecoveryAfterIdentityReservation(t *testing.T) {
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
	data := make([]byte, 16)
	if _, err = rand.Read(data); err != nil {
		t.Fatal(err)
	}
	owner := hex.EncodeToString(data)
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot identify Docker daemon")
	}
	if err = db.ReserveContainerExecution(ctx, task.ID, owner, strings.TrimSpace(string(daemon))); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--network", "none", "--read-only", "--label", "io.pixel-agent-office.execution="+owner, "--entrypoint", "/bin/sh", "postgres:17-alpine", "-c", "setsid sleep 60 & wait").Output()
	if err != nil {
		t.Fatal("cannot start test container")
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
	deadline := time.Now().Add(5 * time.Second)
	for {
		top, topErr := exec.CommandContext(ctx, "docker", "top", id, "-eo", "pid,ppid,pgid,args").Output()
		found := false
		if topErr == nil {
			for _, line := range strings.Split(string(top), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 5 && (fields[3] == "sleep" || fields[3] == "/bin/sleep") && fields[4] == "60" {
					found = true
				}
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("test subprocess did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err = db.FinishTask(ctx, task.ID, "failed", nil, nil, true); err != nil {
		t.Fatal(err)
	}
	queued, err := db.CreateTask(ctx, project.ID, "next", "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = RecoverContainers(ctx, db); err != nil {
		t.Fatal(err)
	}
	execution, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || execution.ContainerID == nil || *execution.ContainerID != id {
		t.Fatalf("identity not recovered: %#v %v", execution, err)
	}
	claimed, err := db.ClaimNext(ctx)
	if err != nil || claimed.Task.ID != queued.ID {
		t.Fatalf("queue not resumed: %#v %v", claimed, err)
	}
}
