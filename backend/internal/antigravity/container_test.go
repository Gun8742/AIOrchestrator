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

func TestStopContainerRejectsUnverifiedOwnership(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("a", 64)
	script := `#!/bin/sh
case "$1" in
  info) printf '%s\n' 'daemon';;
  inspect) printf '{"id":"%s","status":"running","running":true,"restarting":false,"dead":false,"pid":1,"owner":"someone-else","pidMode":"","restart":"no","privileged":false}\n' "$4";;
  kill) printf invoked > "$MARKER";;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARKER", filepath.Join(dir, "killed"))
	err := StopContainer(context.Background(), ContainerRef{ID: id, Owner: "expected", DaemonID: "daemon"})
	if !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "killed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unverified container was targeted: %v", err)
	}
}

func TestDockerStopContainer(t *testing.T) {
	if os.Getenv("DOCKER_CONTAINMENT_TEST") != "1" {
		t.Skip("set DOCKER_CONTAINMENT_TEST=1 to use Docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	owner := hex.EncodeToString(bytes)
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil {
		t.Fatal("cannot inspect Docker daemon")
	}
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--network", "none", "--read-only", "--label", "io.pixel-agent-office.execution="+owner, "--entrypoint", "/bin/sh", "postgres:17-alpine", "-c", "setsid sleep 60 & wait").Output()
	if err != nil {
		t.Fatal("cannot create isolated test container")
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
			t.Fatal("test child process did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	ref := ContainerRef{ID: id, Owner: owner, DaemonID: strings.TrimSpace(string(daemon))}
	found, err := FindContainer(ctx, owner, ref.DaemonID)
	if err != nil || found != ref {
		t.Fatalf("find reserved container: %#v %v", found, err)
	}
	if err = RemoveStoppedContainer(ctx, ref); !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatal("running container was removable")
	}
	if err = StopContainer(ctx, ref); err != nil {
		t.Fatal(err)
	}
	state, err := inspectContainer(ctx, ref)
	if err != nil || *state.Running {
		t.Fatalf("container still running: %v", err)
	}
	if err = StopContainer(ctx, ref); err != nil {
		t.Fatalf("repeat stop: %v", err)
	}
	if err = RemoveStoppedContainer(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if err = exec.CommandContext(ctx, "docker", "inspect", ref.ID).Run(); err == nil {
		t.Fatal("stopped container was not removed")
	}
}

func TestStopContainerFailsClosedWhenKillCannotBeConfirmed(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("b", 64)
	script := `#!/bin/sh
case "$1" in
  info) printf '%s\n' 'daemon';;
  inspect) printf '{"id":"%s","status":"running","running":true,"restarting":false,"dead":false,"pid":1,"owner":"owner","pidMode":"","restart":"no","privileged":false}\n' "$4";;
  kill) exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	err := StopContainer(context.Background(), ContainerRef{ID: id, Owner: "owner", DaemonID: "daemon"})
	if !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatalf("got %v", err)
	}
}

func TestStopContainerRejectsIncompleteInspection(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("c", 64)
	script := `#!/bin/sh
case "$1" in
  info) printf '%s\n' 'daemon';;
  inspect) printf '{"id":"%s","owner":"owner","pidMode":"","restart":"no","privileged":false}\n' "$4";;
  kill) printf invoked > "$MARKER";;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARKER", filepath.Join(dir, "killed"))
	err := StopContainer(context.Background(), ContainerRef{ID: id, Owner: "owner", DaemonID: "daemon"})
	if !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "killed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete inspection targeted container: %v", err)
	}
}

func TestFindContainerRejectsMissingOrAmbiguousResults(t *testing.T) {
	for _, tc := range []struct{ name, output string }{
		{"missing", ""},
		{"multiple", strings.Repeat("a", 64) + "\\n" + strings.Repeat("b", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := "#!/bin/sh\ncase \"$1\" in\n info) printf '%s\\n' daemon;;\n ps) printf '%s\\n' \"$IDS\";;\nesac\n"
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("IDS", strings.ReplaceAll(tc.output, "\\n", "\n"))
			_, err := FindContainer(context.Background(), strings.Repeat("c", 32), "daemon")
			if !errors.Is(err, ErrContainerUnconfirmed) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestStopContainerRejectsCreatedState(t *testing.T) {
	dir := t.TempDir()
	id := strings.Repeat("c", 64)
	script := `#!/bin/sh
case "$1" in
  info) printf '%s\n' 'daemon';;
  inspect) printf '{"id":"%s","status":"created","running":false,"restarting":false,"dead":false,"pid":0,"owner":"owner","pidMode":"","restart":"no","privileged":false}\n' "$4";;
  kill) printf invoked > "$MARKER";;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("MARKER", filepath.Join(dir, "killed"))
	err := StopContainer(context.Background(), ContainerRef{ID: id, Owner: "owner", DaemonID: "daemon"})
	if !errors.Is(err, ErrContainerUnconfirmed) {
		t.Fatalf("got %v", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "killed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created container targeted: %v", err)
	}
}
