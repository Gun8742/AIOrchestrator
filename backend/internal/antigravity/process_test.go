package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunSendsPromptThroughStdin(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
case "$*" in *sensitive-prompt*) exit 9;; esac
printf '{"event":"init","init":{"permission_mode":"request-review","cwd":"%s","model":"gemini-3.8-flash-medium"}}\n' "$PWD"
IFS= read -r request
case "$request" in *sensitive-prompt*) ;; *) exit 8;; esac
printf '%s\n' '{"event":"result","result":{"status":"SUCCESS","response":"OK"}}'
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	result, err := Run(context.Background(), dir, "sensitive-prompt")
	if err != nil || result != "OK" {
		t.Fatalf("result=%q error=%v", result, err)
	}
}

func TestRunRejectsPermissionModeBeforeSendingPrompt(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '%s\n' '{"event":"init","init":{"permission_mode":"always-proceed"}}'
if IFS= read -r request; then printf '%s' "$request" > received; fi
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	_, err := Run(context.Background(), dir, "sensitive-prompt")
	if !errors.Is(err, ErrPermissionMode) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "received")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt reached process: %v", err)
	}
}

func TestRunRejectsPermissionDenialWithoutExposingToolDetails(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '{"event":"init","init":{"permission_mode":"request-review","cwd":"%s","model":"gemini-3.8-flash-medium"}}\n' "$PWD"
IFS= read -r request
printf '%s\n' '{"event":"step_update","step_update":{"step_type":"tool","state":"ERROR","tool_info":{"error":{"message":"sensitive-command"}}}}' '{"event":"result","result":{"status":"SUCCESS","response":"done","denied_actions":[{"action":"command"}]}}'
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	result, err := Run(context.Background(), dir, "test")
	if result != "" || !errors.Is(err, ErrPermissionDenied) || strings.Contains(err.Error(), "sensitive-command") {
		t.Fatalf("unexpected result=%q error=%v", result, err)
	}
}

func TestRunHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, t.TempDir(), "test")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestRealRun(t *testing.T) {
	if os.Getenv("AGY_SMOKE_TEST") != "1" {
		t.Skip("set AGY_SMOKE_TEST=1 to use local agy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := Run(ctx, t.TempDir(), "Reply with exactly OK. Do not use tools or read files.")
	if err != nil || strings.TrimSpace(result) != "OK" {
		t.Fatalf("unexpected response: %v", err)
	}
}

func TestRunRejectsRuntimeMismatchBeforeSendingPrompt(t *testing.T) {
	for _, tc := range []struct{ name, cwd, model string }{
		{"different project", "/", "gemini-3.8-flash-medium"},
		{"different model", "$PWD", "other-model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\nprintf '{\"event\":\"init\",\"init\":{\"permission_mode\":\"request-review\",\"cwd\":\"%s\",\"model\":\"%s\"}}\\n' \"" + tc.cwd + "\" \"" + tc.model + "\"\nif IFS= read -r request; then printf '%s' \"$request\" > received; fi\n"
			if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			_, err := Run(context.Background(), dir, "sensitive-prompt")
			if !errors.Is(err, ErrRuntimeMismatch) {
				t.Fatalf("got %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "received")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prompt reached mismatched runtime: %v", err)
			}
		})
	}
}

func TestCancellationWhileWaitingForInitDoesNotSendPrompt(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf started > started\nif IFS= read -r request; then printf '%s' \"$request\" > received; fi\n"
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, dir, "sensitive-prompt"); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("adapter did not stop after cancellation")
	}
	if _, err := os.Stat(filepath.Join(dir, "received")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt sent before init: %v", err)
	}
}
