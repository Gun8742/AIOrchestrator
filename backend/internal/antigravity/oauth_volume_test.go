package antigravity

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestDockerOAuthVolumePersistsForNonRootUser(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		t.Fatal(err)
	}
	volume := "pixel-oauth-fixture-" + hex.EncodeToString(token[:])
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", volume).Run() })
	for _, script := range []string{
		`[ "$(id -u)" = 1000 ] && [ -w "$HOME/.gemini" ] && printf synthetic > "$HOME/.gemini/persistence-fixture"`,
		`[ "$(cat "$HOME/.gemini/persistence-fixture")" = synthetic ]`,
	} {
		args := []string{"run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "--tmpfs", "/home/agent:rw,nosuid,nodev,uid=1000,gid=1000,mode=0700,size=64m", "--mount", "type=volume,src=" + volume + ",dst=/home/agent/.gemini", "--entrypoint", "/bin/sh", "pixel-agent-office-agy:1.4.9", "-c", script}
		if err := exec.CommandContext(ctx, "docker", args...).Run(); err != nil {
			t.Fatal("non-root OAuth volume persistence check failed")
		}
	}
}
