package antigravity

import (
	"context"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

var ErrContainerCreate = errors.New("antigravity container creation could not be verified")

const createdInspection = `{"id":{{json .Id}},"status":{{json .State.Status}},"owner":{{json (index .Config.Labels "io.pixel-agent-office.execution")}},"image":{{json .Image}},"readonly":{{json .HostConfig.ReadonlyRootfs}},"privileged":{{json .HostConfig.Privileged}},"pidMode":{{json .HostConfig.PidMode}},"restart":{{json .HostConfig.RestartPolicy.Name}},"mounts":{{json .Mounts}},"user":{{json .Config.User}},"command":{{json .Config.Cmd}},"caps":{{json .HostConfig.CapDrop}},"security":{{json .HostConfig.SecurityOpt}},"tmpfs":{{json .HostConfig.Tmpfs}},"network":{{json .HostConfig.NetworkMode}}}`

type createdContainer struct {
	ID         string            `json:"id"`
	Status     string            `json:"status"`
	Owner      string            `json:"owner"`
	Image      string            `json:"image"`
	ReadOnly   *bool             `json:"readonly"`
	Privileged *bool             `json:"privileged"`
	PIDMode    *string           `json:"pidMode"`
	Restart    *string           `json:"restart"`
	User       string            `json:"user"`
	Command    []string          `json:"command"`
	Caps       []string          `json:"caps"`
	Security   []string          `json:"security"`
	Tmpfs      map[string]string `json:"tmpfs"`
	Network    string            `json:"network"`
	Mounts     []struct {
		Type        string `json:"Type"`
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
		RW          bool   `json:"RW"`
		Name        string `json:"Name"`
	} `json:"mounts"`
}

// CreateContainer requires an owner reservation to have been committed before calling it.
func CreateContainer(ctx context.Context, owner, daemonID, imageID, project string) (ContainerRef, error) {
	return createContainer(ctx, owner, daemonID, imageID, project, "")
}

func CreateOAuthContainer(ctx context.Context, owner, daemonID, imageID, project, volume string) (ContainerRef, error) {
	if err := checkOAuthVolume(ctx, volume); err != nil {
		return ContainerRef{}, err
	}
	return createContainer(ctx, owner, daemonID, imageID, project, volume)
}

func createContainer(ctx context.Context, owner, daemonID, imageID, project, volume string) (ContainerRef, error) {
	if !lowerHex(owner, 32) || !strings.HasPrefix(imageID, "sha256:") || !lowerHex(strings.TrimPrefix(imageID, "sha256:"), 64) || daemonID == "" || !filepath.IsAbs(project) || strings.ContainsAny(project, "\r\n") {
		return ContainerRef{}, ErrContainerCreate
	}
	canonical, err := filepath.EvalSymlinks(project)
	if err != nil || canonical != project {
		return ContainerRef{}, ErrContainerCreate
	}
	if err = checkDaemon(ctx, daemonID); err != nil {
		return ContainerRef{}, ErrContainerCreate
	}
	var mount strings.Builder
	writer := csv.NewWriter(&mount)
	if err = writer.Write([]string{"type=bind", "src=" + project, "dst=/workspace"}); err != nil {
		return ContainerRef{}, ErrContainerCreate
	}
	writer.Flush()
	if err = writer.Error(); err != nil {
		return ContainerRef{}, ErrContainerCreate
	}
	args := []string{"create", "--name", "pixel-agent-" + owner, "--label", "io.pixel-agent-office.execution=" + owner, "--interactive", "--network", "bridge", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m", "--tmpfs", "/home/agent:rw,nosuid,nodev,uid=1000,gid=1000,mode=0700,size=64m", "--mount", strings.TrimSuffix(mount.String(), "\n"), imageID, "--model", model, "--sandbox", "--input-format", "stream-json", "--output-format", "stream-json"}
	if volume != "" {
		imageIndex := slices.Index(args, imageID)
		args = slices.Insert(args, imageIndex, "--mount", "type=volume,src="+volume+",dst=/home/agent/.gemini")
	}
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	id := strings.TrimSpace(string(out))
	if err != nil || !lowerHex(id, 64) {
		return ContainerRef{}, ErrContainerCreate
	}
	ref := ContainerRef{ID: id, Owner: owner, DaemonID: daemonID, AuthVolume: volume}
	if err = checkDaemon(ctx, daemonID); err != nil {
		return ContainerRef{}, ErrContainerCreate
	}
	if err = verifyCreatedContainer(ctx, ref, imageID, project); err != nil {
		return ContainerRef{}, err
	}
	return ref, nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func verifyCreatedContainer(ctx context.Context, ref ContainerRef, imageID, project string) error {
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", createdInspection, ref.ID).Output()
	var state createdContainer
	if err != nil || json.Unmarshal(out, &state) != nil || state.ID != ref.ID || state.Status != "created" || state.Owner != ref.Owner || state.Image != imageID || state.ReadOnly == nil || !*state.ReadOnly || state.Privileged == nil || *state.Privileged || state.PIDMode == nil || (*state.PIDMode != "" && *state.PIDMode != "private") || state.Restart == nil || (*state.Restart != "" && *state.Restart != "no") {
		return ErrContainerCreate
	}
	if state.User != "agent" || !slices.Equal(state.Command, []string{"--model", model, "--sandbox", "--input-format", "stream-json", "--output-format", "stream-json"}) || !slices.Contains(state.Caps, "ALL") || !slices.Contains(state.Security, "no-new-privileges") || state.Network != "bridge" || len(state.Tmpfs) != 2 || state.Tmpfs["/tmp"] != "rw,nosuid,nodev,size=64m" || state.Tmpfs["/home/agent"] != "rw,nosuid,nodev,uid=1000,gid=1000,mode=0700,size=64m" {
		return ErrContainerCreate
	}
	expectedMounts := 1
	if ref.AuthVolume != "" {
		expectedMounts++
	}
	if len(state.Mounts) != expectedMounts {
		return ErrContainerCreate
	}
	projectFound, authFound := false, ref.AuthVolume == ""
	for _, mount := range state.Mounts {
		if mount.Type == "bind" && mount.Source == project && mount.Destination == "/workspace" && mount.RW {
			projectFound = true
		} else if ref.AuthVolume != "" && mount.Type == "volume" && mount.Name == ref.AuthVolume && mount.Destination == "/home/agent/.gemini" && mount.RW {
			authFound = true
		} else {
			return ErrContainerCreate
		}
	}
	if !projectFound || !authFound {
		return ErrContainerCreate
	}
	return nil
}
