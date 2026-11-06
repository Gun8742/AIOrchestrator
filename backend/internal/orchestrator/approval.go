package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
)

var ErrApprovalTimeout = errors.New("approval timed out")

type ApprovalStore interface {
	BeginApproval(context.Context, int64, string, json.RawMessage) (store.Approval, error)
	ResolveApproval(context.Context, int64, string, string) error
}
type replyClient interface {
	Reply(json.RawMessage, any) error
}
type answer struct {
	decision string
	done     chan error
}
type pending struct {
	id             string
	answer         chan answer
	submitted      bool
	approveAllowed bool
}

type Approvals struct {
	store   ApprovalStore
	mu      sync.Mutex
	pending map[int64]*pending
}

func NewApprovals(s ApprovalStore) *Approvals {
	return &Approvals{store: s, pending: make(map[int64]*pending)}
}

func (a *Approvals) Handle(ctx context.Context, taskID int64, client replyClient, event codex.Message) error {
	var details struct {
		Command        *string         `json:"command,omitempty"`
		Cwd            *string         `json:"cwd,omitempty"`
		Reason         *string         `json:"reason,omitempty"`
		GrantRoot      *string         `json:"grantRoot,omitempty"`
		Kind           string          `json:"kind,omitempty"`
		Network        json.RawMessage `json:"networkApprovalContext,omitempty"`
		Changes        json.RawMessage `json:"changes,omitempty"`
		ApproveAllowed bool            `json:"approveAllowed"`
	}
	if err := json.Unmarshal(event.Params, &details); err != nil {
		return err
	}
	details.ApproveAllowed = false
	switch event.Method {
	case "item/commandExecution/requestApproval":
		details.ApproveAllowed = (details.Kind == "" || details.Kind == "command") && details.Command != nil && strings.TrimSpace(*details.Command) != "" && details.Cwd != nil && strings.TrimSpace(*details.Cwd) != ""
	case "item/fileChange/requestApproval":
		details.ApproveAllowed = completeFileChanges(details.Changes)
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "GEMINI_API_KEY"} {
		secret := os.Getenv(key)
		if secret != "" && strings.Contains(string(payload), secret) {
			return errors.New("approval contains a known credential")
		}
	}
	a.mu.Lock()
	if _, exists := a.pending[taskID]; exists {
		a.mu.Unlock()
		return errors.New("approval already pending")
	}
	record, err := a.store.BeginApproval(ctx, taskID, event.Method, payload)
	if err != nil {
		a.mu.Unlock()
		return err
	}
	p := &pending{id: record.ID, answer: make(chan answer, 1), approveAllowed: details.ApproveAllowed}
	a.pending[taskID] = p
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, taskID); a.mu.Unlock() }()
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ErrApprovalTimeout
	case response := <-p.answer:
		decision := "decline"
		if response.decision == "approve" {
			decision = "accept"
		}
		err = client.Reply(event.ID, map[string]string{"decision": decision})
		if err == nil {
			err = a.store.ResolveApproval(ctx, taskID, record.ID, response.decision)
		}
		response.done <- err
		return err
	}
}

func (a *Approvals) Respond(ctx context.Context, taskID int64, id, decision string) error {
	if decision != "approve" && decision != "deny" {
		return errors.New("invalid decision")
	}
	a.mu.Lock()
	p, ok := a.pending[taskID]
	if !ok || p.id != id || p.submitted || (decision == "approve" && !p.approveAllowed) {
		a.mu.Unlock()
		return store.ErrConflict
	}
	p.submitted = true
	response := answer{decision: decision, done: make(chan error, 1)}
	select {
	case p.answer <- response:
	default:
		a.mu.Unlock()
		return store.ErrConflict
	}
	a.mu.Unlock()
	select {
	case err := <-response.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func completeFileChanges(raw json.RawMessage) bool {
	var changes []struct {
		Path string  `json:"path"`
		Diff *string `json:"diff"`
		Kind struct {
			Type string `json:"type"`
		} `json:"kind"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &changes) != nil || len(changes) == 0 {
		return false
	}
	for _, change := range changes {
		if strings.TrimSpace(change.Path) == "" || change.Diff == nil {
			return false
		}
		switch change.Kind.Type {
		case "add", "update", "delete":
		default:
			return false
		}
	}
	return true
}
