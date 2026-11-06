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

func TestOAuthContainerTurn(t *testing.T) {
	if os.Getenv("AGY_OAUTH_SMOKE") != "1" {
		t.Skip("set AGY_OAUTH_SMOKE=1 to use AI Pro quota")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot identify Docker daemon")
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", "pixel-agent-office-agy:1.4.9").Output()
	if err != nil {
		t.Fatal("cannot identify OAuth image")
	}
	imageID := strings.TrimSpace(string(image))
	for _, cancelRun := range []bool{false, true} {
		name := "result"
		if cancelRun {
			name = "cancel"
		}
		t.Run(name, func(t *testing.T) {
			var token [16]byte
			if _, err := rand.Read(token[:]); err != nil {
				t.Fatal(err)
			}
			owner := hex.EncodeToString(token[:])
			t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pixel-agent-"+owner).Run() })
			ref, err := CreateOAuthContainer(ctx, owner, strings.TrimSpace(string(daemon)), imageID, project, OAuthVolume)
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
				result, err := RunContainer(runCtx, ref, imageID, project, "Reply exactly PIXEL_OAUTH_BACKEND_OK. Do not use tools or read files.")
				done <- outcome{result, err}
			}()
			if cancelRun {
				deadline := time.Now().Add(10 * time.Second)
				for {
					running, err := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Running}}", ref.ID).Output()
					if err == nil && strings.TrimSpace(string(running)) == "true" {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("container did not start")
					}
					time.Sleep(50 * time.Millisecond)
				}
				stop()
			}
			select {
			case result := <-done:
				if !result.result.Stopped {
					t.Fatal("container stop was not confirmed")
				}
				if cancelRun {
					if !errors.Is(result.err, context.Canceled) {
						t.Fatalf("cancellation: %v", result.err)
					}
				} else if result.err != nil {
					t.Fatal(result.err)
				} else if strings.TrimSpace(result.result.Result) != "PIXEL_OAUTH_BACKEND_OK" {
					t.Fatal("unexpected synthetic response")
				}
			case <-ctx.Done():
				t.Fatal("OAuth runtime timed out")
			}
		})
	}
}
