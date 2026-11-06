package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
)

type approvalFakeStore struct {
	started  chan struct{}
	decision string
}

func (f *approvalFakeStore) BeginApproval(_ context.Context, id int64, kind string, details json.RawMessage) (store.Approval, error) {
	close(f.started)
	return store.Approval{ID: "approval", TaskID: id, Kind: kind, Details: details}, nil
}
func (f *approvalFakeStore) ResolveApproval(_ context.Context, _ int64, _ string, decision string) error {
	f.decision = decision
	return nil
}

type approvalFakeClient struct{ response chan string }

func (f *approvalFakeClient) Reply(_ json.RawMessage, v any) error {
	f.response <- v.(map[string]string)["decision"]
	return nil
}
func TestApprovalResponseIsForwardedOnce(t *testing.T) {
	db := &approvalFakeStore{started: make(chan struct{})}
	client := &approvalFakeClient{response: make(chan string, 1)}
	a := NewApprovals(db)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Handle(ctx, 1, client, codex.Message{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"go test"}`)})
	}()
	<-db.started
	// Wait for the handler's in-memory registration; the database record may be visible first.
	for {
		a.mu.Lock()
		_, ok := a.pending[1]
		a.mu.Unlock()
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("approval not registered")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := a.Respond(ctx, 1, "approval", "deny"); err != nil {
		t.Fatal(err)
	}
	if got := <-client.response; got != "decline" {
		t.Fatalf("decision %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if db.decision != "deny" {
		t.Fatalf("stored decision %q", db.decision)
	}
	if err := a.Respond(ctx, 1, "approval", "approve"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestApprovalRejectsKnownCredentials(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-secret-value")
	db := &approvalFakeStore{started: make(chan struct{})}
	a := NewApprovals(db)
	err := a.Handle(context.Background(), 1, &approvalFakeClient{response: make(chan string, 1)}, codex.Message{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"use test-secret-value"}`)})
	if err == nil {
		t.Fatal("expected credential-bearing approval to be rejected")
	}
	select {
	case <-db.started:
		t.Fatal("credential was persisted")
	default:
	}
}

func TestIncompleteApprovalCannotBeApproved(t *testing.T) {
	db := &approvalFakeStore{started: make(chan struct{})}
	client := &approvalFakeClient{response: make(chan string, 1)}
	a := NewApprovals(db)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- a.Handle(ctx, 1, client, codex.Message{ID: json.RawMessage(`1`), Method: "item/fileChange/requestApproval", Params: json.RawMessage(`{"reason":"write"}`)})
	}()
	<-db.started
	for {
		a.mu.Lock()
		_, ok := a.pending[1]
		a.mu.Unlock()
		if ok {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("approval not registered")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := a.Respond(ctx, 1, "approval", "approve"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("incomplete request allowed: %v", err)
	}
	if err := a.Respond(ctx, 1, "approval", "deny"); err != nil {
		t.Fatal(err)
	}
	if got := <-client.response; got != "decline" {
		t.Fatalf("decision %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestFileChangeCompleteness(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`[{"path":"a.txt","diff":"+hello","kind":{"type":"add"}}]`, true},
		{`[{"path":"a.txt","kind":{"type":"add"}}]`, false},
		{`[{"path":"","diff":"x","kind":{"type":"update"}}]`, false},
		{`[]`, false},
	} {
		if got := completeFileChanges(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("completeFileChanges(%s)=%v", tc.raw, got)
		}
	}
}

type delayedApprovalStore struct {
	entered chan struct{}
	release chan struct{}
}

func (f *delayedApprovalStore) BeginApproval(_ context.Context, id int64, kind string, details json.RawMessage) (store.Approval, error) {
	close(f.entered)
	<-f.release
	return store.Approval{ID: "approval", TaskID: id, Kind: kind, Details: details}, nil
}
func (f *delayedApprovalStore) ResolveApproval(context.Context, int64, string, string) error {
	return nil
}
func TestApprovalVisibleDuringRegistrationCanBeAnswered(t *testing.T) {
	db := &delayedApprovalStore{entered: make(chan struct{}), release: make(chan struct{})}
	a := NewApprovals(db)
	client := &approvalFakeClient{response: make(chan string, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	handled := make(chan error, 1)
	go func() {
		handled <- a.Handle(ctx, 1, client, codex.Message{ID: json.RawMessage(`1`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"command":"echo test","cwd":"/tmp"}`)})
	}()
	<-db.entered
	answered := make(chan error, 1)
	go func() { answered <- a.Respond(ctx, 1, "approval", "approve") }()
	close(db.release)
	if err := <-answered; err != nil {
		t.Fatalf("answer during registration: %v", err)
	}
	if got := <-client.response; got != "accept" {
		t.Fatalf("response %q", got)
	}
	if err := <-handled; err != nil {
		t.Fatal(err)
	}
}
