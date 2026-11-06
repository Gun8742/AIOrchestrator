package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
	"pixel-agent-office/backend/internal/testdb"
)

func TestAntigravityCancelBeforePreparation(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	result, err := ExecuteAntigravity(nil, "", "")(context.Background(), 1, "/tmp", "test", stop)
	if !errors.Is(err, codex.ErrInterrupted) || !result.Terminated {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestAntigravityErrorsAndKnownCredentialRedaction(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "fake-gemini-secret")
	path, err := store.CanonicalDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		execution codex.Execution
		err       error
		status    string
		blocked   bool
		want      string
	}{
		{"permission denied", codex.Execution{Terminated: true}, antigravity.ErrPermissionDenied, "failed", false, "Antigravity denied a requested action; review its permissions"},
		{"unconfirmed container", codex.Execution{}, antigravity.ErrContainerUnconfirmed, "failed", true, "Antigravity container could not be confirmed stopped; project queue remains blocked"},
		{"result redaction", codex.Execution{Terminated: true, Result: "answer fake-gemini-secret"}, nil, "completed", false, "answer [REDACTED]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &fakeQueue{}
			worker := NewWithAntigravity(q, nil, func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error) {
				return tc.execution, tc.err
			})
			err := worker.runTask(context.Background(), store.ClaimedTask{Task: store.Task{ID: 1, Provider: "antigravity"}, ProjectPath: path}, &control{stop: make(chan struct{})})
			if err != nil || q.status != tc.status || q.block != tc.blocked {
				t.Fatalf("error=%v status=%s block=%v", err, q.status, q.block)
			}
			if tc.err != nil && (q.errorText == nil || *q.errorText != tc.want) {
				t.Fatalf("incorrect failure message")
			}
			if tc.err == nil && (q.result == nil || *q.result != tc.want) {
				t.Fatal("result was not redacted")
			}
		})
	}
}

func TestDockerExecuteAntigravityOffline(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	t.Setenv("GEMINI_API_KEY", "fake-gemini-secret")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	db, err := store.Open(ctx, testdb.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 16)
	if _, err = rand.Read(data); err != nil {
		t.Fatal(err)
	}
	tag := "pixel-agent-fixture:" + hex.EncodeToString(data)
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", tag).Run() })
	dir := t.TempDir()
	dockerfile := "FROM pixel-agent-office-agy:1.4.9\nUSER root\nCOPY --chmod=755 fake-agy /usr/local/bin/agy\nUSER agent\n"
	script := `#!/bin/sh
printf '%s\n' '{"event":"init","init":{"permission_mode":"request-review","cwd":"/workspace","model":"gemini-3.8-flash-medium"}}'
IFS= read -r request
printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"answer fake-gemini-secret"}}'
`
	if err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "fake-agy"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err = exec.CommandContext(ctx, "docker", "build", "--network", "none", "-t", tag, dir).Run(); err != nil {
		t.Fatal("cannot build runtime fixture")
	}
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot identify Docker daemon")
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if err != nil {
		t.Fatal("cannot inspect runtime image")
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
	claimed, err := db.ClaimNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `UPDATE tasks SET provider='antigravity' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	claimed.Task.Provider = "antigravity"
	t.Cleanup(func() {
		execution, err := db.ContainerExecution(context.Background(), task.ID)
		if err == nil {
			_ = exec.Command("docker", "rm", "-f", "pixel-agent-"+execution.OwnerToken).Run()
		}
	})
	worker := NewWithAntigravity(db, nil, ExecuteAntigravity(db, strings.TrimSpace(string(daemon)), strings.TrimSpace(string(image))))
	worker.SetAntigravityCleanup(AntigravityCleanup(db))
	if err = worker.runTask(ctx, claimed, &control{stop: make(chan struct{})}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.Task(ctx, task.ID)
	if err != nil || snapshot.Task.Status != "completed" || snapshot.Task.Result == nil || *snapshot.Task.Result != "answer [REDACTED]" {
		t.Fatalf("task: %#v %v", snapshot.Task, err)
	}
	execution, err := db.ContainerExecution(ctx, task.ID)
	if err != nil || execution.ContainerID == nil {
		t.Fatal("missing durable container identity")
	}
	if err = exec.CommandContext(ctx, "docker", "inspect", *execution.ContainerID).Run(); err == nil {
		t.Fatal("terminal container was not cleaned up")
	}
	for _, event := range snapshot.Events {
		if strings.Contains(string(event.Payload), "fake-gemini-secret") {
			t.Fatal("credential leaked into event")
		}
	}
}
