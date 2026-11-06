package antigravity

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"strings"
)

const OAuthVolume = "pixel-agent-office-agy-auth"

type OAuthRuntime struct {
	DaemonID string
	ImageID  string
	Volume   string
}

// LoadOAuthRuntime checks installation; account access is checked again when execution starts.
func LoadOAuthRuntime(ctx context.Context) (OAuthRuntime, error) {
	var runtime OAuthRuntime
	daemon, err := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ID}}").Output()
	if err != nil || strings.TrimSpace(string(daemon)) == "" {
		return runtime, ErrContainerCreate
	}
	image, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", `{"id":{{json .Id}},"auth":{{json (index .Config.Labels "io.pixel-agent-office.auth")}},"entrypoint":{{json .Config.Entrypoint}}}`, "pixel-agent-office-agy:1.4.9").Output()
	var state struct {
		ID         string   `json:"id"`
		Auth       string   `json:"auth"`
		Entrypoint []string `json:"entrypoint"`
	}
	if err != nil || json.Unmarshal(image, &state) != nil || !strings.HasPrefix(state.ID, "sha256:") || !lowerHex(strings.TrimPrefix(state.ID, "sha256:"), 64) || state.Auth != "google-oauth-no-overage-v1" || !slices.Equal(state.Entrypoint, []string{"/usr/local/bin/antigravity-entrypoint"}) {
		return runtime, ErrContainerCreate
	}
	if err = checkOAuthVolume(ctx, OAuthVolume); err != nil {
		return runtime, err
	}
	runtime = OAuthRuntime{DaemonID: strings.TrimSpace(string(daemon)), ImageID: state.ID, Volume: OAuthVolume}
	if err = checkDaemon(ctx, runtime.DaemonID); err != nil {
		return OAuthRuntime{}, err
	}
	return runtime, nil
}

func checkOAuthVolume(ctx context.Context, volume string) error {
	if volume == "" || strings.ContainsAny(volume, ",/\\\r\n ") {
		return ErrContainerCreate
	}
	out, err := exec.CommandContext(ctx, "docker", "volume", "inspect", "--format", `{"name":{{json .Name}},"purpose":{{json (index .Labels "io.pixel-agent-office.purpose")}}}`, volume).Output()
	var state struct {
		Name    string `json:"name"`
		Purpose string `json:"purpose"`
	}
	if err != nil || json.Unmarshal(out, &state) != nil || state.Name != volume || state.Purpose != "antigravity-oauth" {
		return ErrContainerCreate
	}
	return nil
}
