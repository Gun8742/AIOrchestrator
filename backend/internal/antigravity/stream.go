package antigravity

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrPermissionDenied = errors.New("antigravity denied a requested action")
var ErrToolFailed = errors.New("antigravity tool failed")
var ErrIncomplete = errors.New("antigravity stream did not finish successfully")
var ErrPermissionMode = errors.New("antigravity permission mode is unsupported")
var ErrRuntimeMismatch = errors.New("antigravity runtime project or model does not match")

type streamEvent struct {
	Event string `json:"event"`
	Init  struct {
		PermissionMode string `json:"permission_mode"`
		Cwd            string `json:"cwd"`
		Model          string `json:"model"`
	} `json:"init"`
	Step struct {
		Type     string `json:"step_type"`
		State    string `json:"state"`
		ToolInfo struct {
			Error json.RawMessage `json:"error"`
		} `json:"tool_info"`
	} `json:"step_update"`
	Result struct {
		Status        string            `json:"status"`
		Response      string            `json:"response"`
		DeniedActions []json.RawMessage `json:"denied_actions"`
	} `json:"result"`
}

// ReadResult interprets one headless stream without exposing tool inputs or stderr.
func ReadResult(input io.Reader) (string, error) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	initialized, finished, failedTool := false, false, false
	var output string
	for scanner.Scan() {
		var event streamEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return "", ErrIncomplete
		}
		switch event.Event {
		case "init":
			if initialized || finished {
				return "", ErrIncomplete
			}
			if event.Init.PermissionMode != "request-review" {
				return "", ErrPermissionMode
			}
			initialized = true
		case "step_update":
			if !initialized || finished {
				return "", ErrIncomplete
			}
			switch event.Step.Type {
			case "user_input", "agent_response", "tool", "checkpoint":
			default:
				return "", ErrIncomplete
			}
			switch event.Step.State {
			case "ACTIVE", "DONE":
			case "ERROR":
				failedTool = true
			default:
				return "", ErrIncomplete
			}
			if len(event.Step.ToolInfo.Error) > 0 && string(event.Step.ToolInfo.Error) != "null" {
				failedTool = true
			}
		case "result":
			if !initialized || finished {
				return "", ErrIncomplete
			}
			finished = true
			if len(event.Result.DeniedActions) > 0 {
				return "", ErrPermissionDenied
			}
			if event.Result.Status != "SUCCESS" || strings.TrimSpace(event.Result.Response) == "" {
				return "", ErrIncomplete
			}
			output = event.Result.Response
		default:
			return "", ErrIncomplete
		}
	}
	if scanner.Err() != nil || !finished {
		return "", ErrIncomplete
	}
	if failedTool {
		return "", ErrToolFailed
	}
	return output, nil
}
