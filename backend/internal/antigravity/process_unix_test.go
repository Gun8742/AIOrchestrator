//go:build darwin || linux

package antigravity

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelStopsChildInProcessGroup(t *testing.T) {
	dir := t.TempDir()
	script := `#!/bin/sh
printf '{"event":"init","init":{"permission_mode":"request-review","cwd":"%s","model":"gemini-3.8-flash-medium"}}\n' "$PWD"
IFS= read -r request
/bin/sh -c '/bin/sleep 2; printf survived > survived' &
printf started > started
wait
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, dir, "synthetic")
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
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
		t.Fatal("adapter did not stop")
	}
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "survived")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("child continued after cancellation: %v", err)
	}
}
