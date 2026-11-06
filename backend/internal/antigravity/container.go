package antigravity

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
)

var ErrContainerUnconfirmed = errors.New("antigravity container termination could not be confirmed")

const containerInspection = `{"id":{{json .Id}},"status":{{json .State.Status}},"running":{{json .State.Running}},"restarting":{{json .State.Restarting}},"dead":{{json .State.Dead}},"pid":{{json .State.Pid}},"owner":{{json (index .Config.Labels "io.pixel-agent-office.execution")}},"pidMode":{{json .HostConfig.PidMode}},"restart":{{json .HostConfig.RestartPolicy.Name}},"privileged":{{json .HostConfig.Privileged}}}`

type ContainerRef struct {
	ID         string
	Owner      string
	DaemonID   string
	AuthVolume string
}

type containerState struct {
	ID         string  `json:"id"`
	Status     *string `json:"status"`
	Running    *bool   `json:"running"`
	Restarting *bool   `json:"restarting"`
	Dead       *bool   `json:"dead"`
	PID        *int    `json:"pid"`
	Owner      string  `json:"owner"`
	PIDMode    *string `json:"pidMode"`
	Restart    *string `json:"restart"`
	Privileged *bool   `json:"privileged"`
}

// FindContainer resolves a reserved owner only when exactly one verified container exists.
func FindContainer(ctx context.Context, owner, daemonID string) (ContainerRef, error) {
	if len(owner) != 32 || owner != strings.ToLower(owner) || daemonID == "" {
		return ContainerRef{}, ErrContainerUnconfirmed
	}
	if _, err := hex.DecodeString(owner); err != nil {
		return ContainerRef{}, ErrContainerUnconfirmed
	}
	if err := checkDaemon(ctx, daemonID); err != nil {
		return ContainerRef{}, err
	}
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "--no-trunc", "--filter", "label=io.pixel-agent-office.execution="+owner).Output()
	ids := strings.Fields(string(out))
	if err != nil || len(ids) != 1 || len(ids[0]) != 64 || ids[0] != strings.ToLower(ids[0]) {
		return ContainerRef{}, ErrContainerUnconfirmed
	}
	if _, err = hex.DecodeString(ids[0]); err != nil {
		return ContainerRef{}, ErrContainerUnconfirmed
	}
	ref := ContainerRef{ID: ids[0], Owner: owner, DaemonID: daemonID}
	if _, err = inspectContainer(ctx, ref); err != nil {
		return ContainerRef{}, err
	}
	if err = checkDaemon(ctx, daemonID); err != nil {
		return ContainerRef{}, err
	}
	return ref, nil
}

// StopContainer only confirms a stopped container on the same Docker daemon.
func StopContainer(ctx context.Context, ref ContainerRef) error {
	if len(ref.ID) != 64 || ref.Owner == "" || ref.DaemonID == "" {
		return ErrContainerUnconfirmed
	}
	if err := checkDaemon(ctx, ref.DaemonID); err != nil {
		return err
	}
	state, err := inspectContainer(ctx, ref)
	if err != nil {
		return err
	}
	if *state.Running {
		// A failed kill may race a normal exit; inspection below decides the result.
		_ = exec.CommandContext(ctx, "docker", "kill", ref.ID).Run()
	}
	if err = checkDaemon(ctx, ref.DaemonID); err != nil {
		return err
	}
	state, err = inspectContainer(ctx, ref)
	if err != nil || *state.Running {
		return ErrContainerUnconfirmed
	}
	return nil
}

// RemoveStoppedContainer preserves the OAuth volume and refuses to force-remove a running execution.
func RemoveStoppedContainer(ctx context.Context, ref ContainerRef) error {
	if err := checkDaemon(ctx, ref.DaemonID); err != nil {
		return err
	}
	state, err := inspectContainer(ctx, ref)
	if err != nil || *state.Running {
		return ErrContainerUnconfirmed
	}
	if err = exec.CommandContext(ctx, "docker", "rm", ref.ID).Run(); err != nil {
		return ErrContainerUnconfirmed
	}
	return checkDaemon(ctx, ref.DaemonID)
}

func checkDaemon(ctx context.Context, expected string) error {
	out, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil || strings.TrimSpace(string(out)) != expected {
		return ErrContainerUnconfirmed
	}
	return nil
}

func inspectContainer(ctx context.Context, ref ContainerRef) (containerState, error) {
	var state containerState
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format", containerInspection, ref.ID).Output()
	if err != nil || json.Unmarshal(out, &state) != nil || state.ID != ref.ID || state.Owner != ref.Owner || state.Status == nil || state.Running == nil || state.Restarting == nil || state.Dead == nil || state.PID == nil || state.Privileged == nil || state.PIDMode == nil || state.Restart == nil {
		return containerState{}, ErrContainerUnconfirmed
	}
	if (*state.Running && *state.Status != "running") || (!*state.Running && *state.Status != "exited") || *state.Restarting || *state.Dead || *state.Privileged || (*state.Running && *state.PID <= 0) || (!*state.Running && *state.PID != 0) || (*state.PIDMode != "" && *state.PIDMode != "private") || (*state.Restart != "" && *state.Restart != "no") {
		return containerState{}, ErrContainerUnconfirmed
	}
	return state, nil
}
