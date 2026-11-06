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

func TestCreateContainerRejectsInvalidInputs(t *testing.T) {
	_, err := CreateContainer(context.Background(), "invalid", "daemon", "sha256:"+strings.Repeat("a", 64), "/tmp")
	if !errors.Is(err, ErrContainerCreate) {
		t.Fatalf("got %v", err)
	}
}

func TestDockerCreateContainer(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := t.TempDir()
	project := filepath.Join(base, "project,fixture")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	project, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 16)
	if _, err = rand.Read(data); err != nil {
		t.Fatal(err)
	}
	owner := hex.EncodeToString(data)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "pixel-agent-"+owner).Run() })
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot identify Docker daemon")
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", "pixel-agent-office-agy:1.4.9").Output()
	if err != nil {
		t.Fatal("build the Antigravity test image first")
	}
	ref, err := CreateContainer(ctx, owner, strings.TrimSpace(string(daemon)), strings.TrimSpace(string(image)), project)
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.ID) != 64 || ref.Owner != owner {
		t.Fatalf("bad container reference: %#v", ref)
	}
	if err = StopContainer(ctx, ref); !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatalf("created container must not be considered stopped: %v", err)
	}
}
