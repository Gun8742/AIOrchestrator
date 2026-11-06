package antigravity

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
)

func TestDockerRunContainer(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	t.Setenv("GEMINI_API_KEY", "synthetic-container-key")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	tag := "pixel-agent-fixture:" + hex.EncodeToString(data)
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", tag).Run() })
	dir := t.TempDir()
	dockerfile := "FROM pixel-agent-office-agy:1.4.9\nUSER root\nCOPY --chmod=755 fake-agy /usr/local/bin/agy\nUSER agent\n"
	script := `#!/bin/sh
[ -z "${GEMINI_API_KEY:-}" ] || exit 21
[ "$(cat "$HOME/.gemini/antigravity-cli/settings.json")" = '{"useG1Credits":false}' ] || exit 22
if [ -e /workspace/mismatch ]; then chosen=wrong; else chosen=gemini-3.8-flash-medium; fi
printf '{"event":"init","init":{"permission_mode":"request-review","cwd":"/workspace","model":"%s"}}\n' "$chosen"
if IFS= read -r request; then printf received > /workspace/received; fi
if [ -e /workspace/hang ]; then setsid sleep 60 & wait; fi
printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"fixture answer"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fake-agy"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	if err := exec.CommandContext(ctx, "docker", "build", "--network", "none", "-t", tag, dir).Run(); err != nil {
		t.Fatal("cannot build isolated runtime fixture")
	}
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot inspect Docker daemon")
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag).Output()
	if err != nil {
		t.Fatal("cannot inspect fixture image")
	}
	for _, mismatch := range []bool{false, true} {
		name := "valid"
		if mismatch {
			name = "wrong model"
		}
		t.Run(name, func(t *testing.T) {
			project, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if mismatch {
				if err = os.WriteFile(filepath.Join(project, "mismatch"), []byte("fixture"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			token := make([]byte, 16)
			if _, err = rand.Read(token); err != nil {
				t.Fatal(err)
			}
			owner := hex.EncodeToString(token)
			t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pixel-agent-"+owner).Run() })
			ref, err := CreateContainer(ctx, owner, strings.TrimSpace(string(daemon)), strings.TrimSpace(string(image)), project)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RunContainer(ctx, ref, strings.TrimSpace(string(image)), project, "synthetic prompt")
			if mismatch {
				if !errors.Is(err, ErrRuntimeMismatch) || !result.Stopped {
					t.Fatalf("result=%#v error=%v", result, err)
				}
				if _, err = os.Stat(filepath.Join(project, "received")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("prompt reached mismatched runtime")
				}
			} else if err != nil || !result.Stopped || result.Result != "fixture answer" {
				t.Fatalf("result=%#v error=%v", result, err)
			}
		})
	}
	t.Run("cancel while subprocess runs", func(t *testing.T) {
		project, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(project, "hang"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		token := make([]byte, 16)
		if _, err = rand.Read(token); err != nil {
			t.Fatal(err)
		}
		owner := hex.EncodeToString(token)
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pixel-agent-"+owner).Run() })
		ref, err := CreateContainer(ctx, owner, strings.TrimSpace(string(daemon)), strings.TrimSpace(string(image)), project)
		if err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		type outcome struct {
			result ContainerResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			result, err := RunContainer(runCtx, ref, strings.TrimSpace(string(image)), project, "synthetic prompt")
			done <- outcome{result, err}
		}()
		deadline := time.Now().Add(8 * time.Second)
		for {
			top, topErr := exec.CommandContext(ctx, "docker", "top", ref.ID, "-eo", "pid,ppid,pgid,args").Output()
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
				t.Fatal("fixture subprocess did not start")
			}
			time.Sleep(50 * time.Millisecond)
		}
		stop()
		select {
		case outcome := <-done:
			if !errors.Is(outcome.err, context.Canceled) || !outcome.result.Stopped {
				t.Fatalf("result=%#v error=%v", outcome.result, outcome.err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("container cancellation did not finish")
		}
	})

}
