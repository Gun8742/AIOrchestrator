package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrApprovalUnavailable = errors.New("interactive approval is not available")
var ErrInterrupted = errors.New("codex turn interrupted")
var ErrModelUnavailable = errors.New("codex turn failed: model unavailable")
var ErrUsageLimit = errors.New("codex turn failed: usage limit")
var ErrAuthentication = errors.New("codex turn failed: authentication or access")
var ErrNetwork = errors.New("codex turn failed: network or stream")
var ErrSandbox = errors.New("codex turn failed: sandbox")

type RPC interface {
	Call(context.Context, string, any, any) error
	Events() <-chan Message
	Done() <-chan struct{}
}

type Execution struct {
	ThreadID   string
	TurnID     string
	Result     string
	Terminated bool
}

// Run waits for an authoritative terminal turn event rather than assuming that
// receiving a turn/start response means execution succeeded.
func Run(ctx context.Context, client RPC, project, prompt string) (Execution, error) {
	return RunWithApprovals(ctx, client, project, prompt, nil)
}

type ApprovalHandler func(context.Context, Message) error

func RunWithApprovals(ctx context.Context, client RPC, project, prompt string, approval ApprovalHandler) (Execution, error) {
	return RunControlled(ctx, client, project, prompt, approval, nil, nil)
}

func RunControlled(ctx context.Context, client RPC, project, prompt string, approval ApprovalHandler, stop <-chan struct{}, activity func(context.Context, string, string, map[string]string) error) (Execution, error) {
	var execution Execution
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	params := map[string]any{"cwd": project, "sandbox": "workspace-write", "approvalPolicy": "on-request", "ephemeral": true}
	if err := client.Call(ctx, "thread/start", params, &thread); err != nil {
		return execution, fmt.Errorf("start thread: %w", classifyRPCError(err))
	}
	if thread.Thread.ID == "" {
		return execution, errors.New("codex returned an empty thread ID")
	}
	execution.ThreadID = thread.Thread.ID
	var start struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	input := []map[string]string{{"type": "text", "text": prompt}}
	if err := client.Call(ctx, "turn/start", map[string]any{"threadId": execution.ThreadID, "input": input}, &start); err != nil {
		return execution, fmt.Errorf("start turn: %w", classifyRPCError(err))
	}
	if start.Turn.ID == "" {
		return execution, errors.New("codex returned an empty turn ID")
	}
	execution.TurnID = start.Turn.ID
	var approvalDone <-chan error
	var cancelApproval context.CancelFunc
	defer func() {
		if cancelApproval != nil {
			cancelApproval()
		}
	}()
	var interruptDone <-chan error
	var deadline <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	interrupted := false
	fileChanges := make(map[string]json.RawMessage)
	for {
		select {
		case <-ctx.Done():
			return execution, ctx.Err()
		case <-client.Done():
			return execution, errors.New("codex disconnected before turn completed")
		case <-stop:
			stop = nil
			interrupted = true
			if cancelApproval != nil {
				cancelApproval()
			}
			done := make(chan error, 1)
			interruptDone = done
			go func() {
				done <- client.Call(ctx, "turn/interrupt", map[string]string{"threadId": execution.ThreadID, "turnId": execution.TurnID}, nil)
			}()
			timer = time.NewTimer(15 * time.Second)
			deadline = timer.C
		case <-interruptDone:
			// The turn may have completed while the interrupt was in flight; wait for its terminal event.
			interruptDone = nil
		case <-deadline:
			return execution, errors.New("timed out waiting for interruption")
		case err := <-approvalDone:
			approvalDone = nil
			cancelApproval = nil
			if err != nil && !(interrupted && errors.Is(err, context.Canceled)) {
				return execution, err
			}
		case event := <-client.Events():
			if event.Method != "" && len(event.ID) > 0 {
				if approval == nil {
					return execution, ErrApprovalUnavailable
				}
				if event.Method != "item/commandExecution/requestApproval" && event.Method != "item/fileChange/requestApproval" {
					return execution, ErrApprovalUnavailable
				}
				var ref struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
				}
				if err := json.Unmarshal(event.Params, &ref); err != nil || ref.ThreadID != execution.ThreadID || ref.TurnID != execution.TurnID {
					return execution, errors.New("invalid approval request")
				}
				if event.Method == "item/fileChange/requestApproval" {
					var fields map[string]json.RawMessage
					if err := json.Unmarshal(event.Params, &fields); err != nil {
						return execution, err
					}
					var itemID string
					_ = json.Unmarshal(fields["itemId"], &itemID)
					if changes, ok := fileChanges[itemID]; ok {
						fields["changes"] = changes
						var err error
						event.Params, err = json.Marshal(fields)
						if err != nil {
							return execution, err
						}
					}
				}
				if interrupted {
					continue
				}
				if approvalDone != nil {
					return execution, errors.New("multiple simultaneous approval requests")
				}
				approvalCtx, cancel := context.WithCancel(ctx)
				cancelApproval = cancel
				done := make(chan error, 1)
				approvalDone = done
				go func() { done <- approval(approvalCtx, event) }()
				continue
			}
			switch event.Method {
			case "item/started", "item/completed":
				var value struct {
					ThreadID string `json:"threadId"`
					TurnID   string `json:"turnId"`
					Item     struct {
						Type          string          `json:"type"`
						Text          string          `json:"text"`
						ID            string          `json:"id"`
						Changes       json.RawMessage `json:"changes"`
						Kind          string          `json:"kind"`
						AgentThreadID string          `json:"agentThreadId"`
					} `json:"item"`
				}
				if json.Unmarshal(event.Params, &value) == nil && value.ThreadID == execution.ThreadID && value.TurnID == execution.TurnID {
					if value.Item.Type == "fileChange" && value.Item.ID != "" && len(value.Item.Changes) > 0 && string(value.Item.Changes) != "null" {
						fileChanges[value.Item.ID] = value.Item.Changes
					}
					if event.Method == "item/completed" && value.Item.Type == "agentMessage" {
						execution.Result = value.Item.Text
					}
					switch value.Item.Type {
					case "agentMessage", "commandExecution", "fileChange", "mcpToolCall", "reasoning", "collabAgentToolCall", "subAgentActivity":
						if activity != nil {
							phase := "started"
							if event.Method == "item/completed" {
								phase = "completed"
							}
							metadata := map[string]string{}
							if value.Item.Type == "subAgentActivity" && value.Item.AgentThreadID != "" {
								switch value.Item.Kind {
								case "started", "interacted", "interrupted", "completed":
									metadata["agentThreadId"] = value.Item.AgentThreadID
									metadata["parentThreadId"] = value.ThreadID
									metadata["kind"] = value.Item.Kind
								}
							}
							if err := activity(ctx, phase, value.Item.Type, metadata); err != nil {
								return execution, errors.New("cannot record agent activity")
							}
						}
					}
				}
			case "turn/completed":
				var value struct {
					ThreadID string `json:"threadId"`
					Turn     struct {
						ID     string `json:"id"`
						Status string `json:"status"`
						Error  *struct {
							Message        string          `json:"message"`
							CodexErrorInfo json.RawMessage `json:"codexErrorInfo"`
						} `json:"error"`
					} `json:"turn"`
				}
				if json.Unmarshal(event.Params, &value) != nil || value.ThreadID != execution.ThreadID || value.Turn.ID != execution.TurnID {
					continue
				}
				if approvalDone != nil {
					if interrupted && cancelApproval != nil {
						cancelApproval()
					}
					timer := time.NewTimer(5 * time.Second)
					select {
					case err := <-approvalDone:
						timer.Stop()
						approvalDone = nil
						if err != nil && !(interrupted && errors.Is(err, context.Canceled)) {
							return execution, err
						}
					case <-ctx.Done():
						timer.Stop()
						return execution, ctx.Err()
					case <-timer.C:
						return execution, errors.New("approval did not finish before turn completion")
					}
				}
				execution.Terminated = true
				switch value.Turn.Status {
				case "completed":
					return execution, nil
				case "interrupted":
					return execution, ErrInterrupted
				default:
					if value.Turn.Error != nil {
						if classified := classifyError(value.Turn.Error.Message, string(value.Turn.Error.CodexErrorInfo)); classified != nil {
							return execution, classified
						}
					}
					return execution, errors.New("codex turn failed: unclassified")
				}
			}
		}
	}
}

func classifyRPCError(err error) error {
	var rpc *RPCError
	if errors.As(err, &rpc) {
		if classified := classifyError(rpc.Message, ""); classified != nil {
			return classified
		}
	}
	return err
}

func classifyError(message, info string) error {
	message, info = strings.ToLower(message), strings.ToLower(info)
	switch {
	case strings.Contains(info, "usage") || strings.Contains(info, "quota") || strings.Contains(info, "ratelimit"):
		return ErrUsageLimit
	case strings.Contains(info, "unauthorized"):
		return ErrAuthentication
	case strings.Contains(info, "network") || strings.Contains(info, "connection"):
		return ErrNetwork
	case strings.Contains(message, "auth") || strings.Contains(message, "401") || strings.Contains(message, "403"):
		return ErrAuthentication
	case strings.Contains(message, "quota") || strings.Contains(message, "rate limit") || strings.Contains(message, "usage limit") || strings.Contains(message, "429"):
		return ErrUsageLimit
	case strings.Contains(message, "model"):
		return ErrModelUnavailable
	case strings.Contains(message, "sandbox"):
		return ErrSandbox
	case strings.Contains(message, "connection") || strings.Contains(message, "network") || strings.Contains(message, "stream"):
		return ErrNetwork
	default:
		return nil
	}
}
