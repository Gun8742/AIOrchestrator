package antigravity

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestReadResult(t *testing.T) {
	tests := []struct {
		name, input, output string
		want                error
	}{
		{"success", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"OK"}}`, "OK", nil},
		{"denied despite success", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"","denied_actions":[{"action":"command"}]}}`, "", ErrPermissionDenied},
		{"tool error despite success", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"step_update","step_update":{"step_type":"tool","state":"ERROR"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"done"}}`, "", ErrToolFailed},
		{"error payload despite done", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"step_update","step_update":{"step_type":"tool","state":"DONE","tool_info":{"error":{"message":"private"}}}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"done"}}`, "", ErrToolFailed},
		{"missing step payload", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"step_update"}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"done"}}`, "", ErrIncomplete},
		{"unknown step state", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"step_update","step_update":{"step_type":"tool","state":"UNKNOWN"}}` + "\n" + `{"event":"result","result":{"status":"SUCCESS","response":"done"}}`, "", ErrIncomplete},
		{"error terminal after interrupt", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `{"event":"result","result":{"status":"ERROR"}}`, "", ErrIncomplete},
		{"missing result", `{"event":"init","init":{"permission_mode":"request-review"}}`, "", ErrIncomplete},
		{"missing init", `{"event":"result","result":{"status":"SUCCESS","response":"OK"}}`, "", ErrIncomplete},
		{"malformed", `{"event":"init","init":{"permission_mode":"request-review"}}` + "\n" + `not json`, "", ErrIncomplete},
		{"automatic permissions", `{"event":"init","init":{"permission_mode":"always-proceed"}}`, "", ErrPermissionMode},
		{"missing permission mode", `{"event":"init"}`, "", ErrPermissionMode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadResult(strings.NewReader(tt.input))
			if got != tt.output || !errors.Is(err, tt.want) {
				t.Fatalf("got output=%q error=%v", got, err)
			}
		})
	}
}

func TestRealStream(t *testing.T) {
	if os.Getenv("AGY_SMOKE_TEST") != "1" {
		t.Skip("set AGY_SMOKE_TEST=1 to use local agy")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "agy", "--model", "gemini-3.8-flash-medium", "--mode", "plan", "--sandbox", "--input-format", "stream-json", "--output-format", "stream-json")
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader(`{"event":"user","message":{"content":"Reply with exactly OK. Do not use tools or read files."}}` + "\n")
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("agy execution failed: %v", err)
	}
	result, err := ReadResult(strings.NewReader(string(data)))
	if err != nil || strings.TrimSpace(result) != "OK" {
		t.Fatalf("unexpected parsed response: %v", err)
	}
}
