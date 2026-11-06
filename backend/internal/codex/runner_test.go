package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeRPC struct {
	events chan Message
	done   chan struct{}
}

type observingRPC struct {
	RPC
	events chan Message
}

func (o observingRPC) Events() <-chan Message { return o.events }

func (f *fakeRPC) Call(_ context.Context, method string, _ any, result any) error {
	if method == "thread/start" {
		return json.Unmarshal([]byte(`{"thread":{"id":"thread"}}`), result)
	}
	if method == "turn/start" {
		return json.Unmarshal([]byte(`{"turn":{"id":"turn"}}`), result)
	}
	if method == "turn/interrupt" {
		f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"interrupted"}}`)}
		return nil
	}
	return errors.New("unexpected method")
}
func (f *fakeRPC) Events() <-chan Message { return f.events }
func (f *fakeRPC) Done() <-chan struct{}  { return f.done }
func TestRunUsesMatchingTerminalEvent(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 3), done: make(chan struct{})}
	f.events <- Message{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"agentMessage","text":"done"}}`)}
	f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"other","turn":{"id":"turn","status":"completed"}}`)}
	f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
	result, err := Run(context.Background(), f, "/tmp", "test")
	if err != nil || result.Result != "done" {
		t.Fatalf("result %#v: %v", result, err)
	}
}
func TestRunDoesNotApproveRequests(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 1), done: make(chan struct{})}
	f.events <- Message{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval"}
	_, err := Run(context.Background(), f, "/tmp", "test")
	if !errors.Is(err, ErrApprovalUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func TestAppServerTurn(t *testing.T) {
	if os.Getenv("CODEX_SMOKE_TEST") != "1" {
		t.Skip("set CODEX_SMOKE_TEST=1 to use local Codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var activities []string
	result, err := RunControlled(ctx, smokeModel{RPC: client, model: os.Getenv("CODEX_TEST_MODEL")}, t.TempDir(), "Reply with the single word OK. Do not use any tools.", nil, nil, func(_ context.Context, phase, kind string, _ map[string]string) error {
		activities = append(activities, phase+":"+kind)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.Result) == "" {
		t.Fatal("empty agent response")
	}
	if len(activities) == 0 {
		t.Fatal("no activity events received")
	}
	t.Logf("Received %d activity events", len(activities))
}

func TestAppServerSubagent(t *testing.T) {
	if os.Getenv("CODEX_SMOKE_SUBAGENT") != "1" {
		t.Skip("set CODEX_SMOKE_SUBAGENT=1 to use local Codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var seen bool
	observed := observingRPC{RPC: client, events: make(chan Message)}
	go func() {
		defer close(observed.events)
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.Done():
				return
			case event := <-client.Events():
				if event.Method == "item/started" || event.Method == "item/completed" {
					var item struct {
						Item struct {
							Type              string                     `json:"type"`
							Tool              string                     `json:"tool"`
							Kind              string                     `json:"kind"`
							AgentThreadID     string                     `json:"agentThreadId"`
							Status            string                     `json:"status"`
							SenderThreadID    string                     `json:"senderThreadId"`
							ReceiverThreadIDs []string                   `json:"receiverThreadIds"`
							AgentsStates      map[string]json.RawMessage `json:"agentsStates"`
						} `json:"item"`
					}
					if json.Unmarshal(event.Params, &item) == nil && item.Item.Type == "collabAgentToolCall" {
						t.Logf("%s tool=%s status=%s senderPresent=%t receivers=%d agentStates=%d", event.Method, item.Item.Tool, item.Item.Status, item.Item.SenderThreadID != "", len(item.Item.ReceiverThreadIDs), len(item.Item.AgentsStates))
					}
					if item.Item.Type == "subAgentActivity" {
						t.Logf("%s kind=%s agentThreadPresent=%t", event.Method, item.Item.Kind, item.Item.AgentThreadID != "")
					}
				}
				select {
				case observed.events <- event:
				case <-ctx.Done():
					return
				case <-client.Done():
					return
				}
			}
		}
	}()
	_, err = RunControlled(ctx, observed, t.TempDir(), "Spawn a subagent to answer what is 2+2, wait for its result, and report it. Do not read or write files.", nil, nil, func(_ context.Context, _, kind string, _ map[string]string) error {
		if kind == "collabAgentToolCall" || kind == "subAgentActivity" {
			seen = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no subagent activity received")
	}
}

// smokeModel only overrides the model for an explicit diagnostic run.
type smokeModel struct {
	RPC
	model string
}

func (s smokeModel) Call(ctx context.Context, method string, params any, result any) error {
	if method == "thread/start" && s.model != "" {
		source, ok := params.(map[string]any)
		if !ok {
			return errors.New("invalid start parameters")
		}
		clone := make(map[string]any, len(source)+1)
		for k, v := range source {
			clone[k] = v
		}
		clone["model"] = s.model
		params = clone
	}
	return s.RPC.Call(ctx, method, params, result)
}

func TestRunPassesApprovalToHandler(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 2), done: make(chan struct{})}
	f.events <- Message{ID: json.RawMessage(`7`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn"}`)}
	called := make(chan bool, 1)
	_, err := RunWithApprovals(context.Background(), f, "/tmp", "test", func(_ context.Context, m Message) error {
		called <- string(m.ID) == "7"
		f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
		return nil
	})
	if err != nil || !<-called {
		t.Fatalf("approval callback: %v", err)
	}
}

func TestInterruptWhileApprovalPending(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 2), done: make(chan struct{})}
	f.events <- Message{ID: json.RawMessage(`8`), Method: "item/commandExecution/requestApproval", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn"}`)}
	stop := make(chan struct{})
	started := make(chan struct{})
	go func() { <-started; close(stop) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := RunControlled(ctx, f, "/tmp", "test", func(ctx context.Context, _ Message) error { close(started); <-ctx.Done(); return ctx.Err() }, stop, nil)
	if !errors.Is(err, ErrInterrupted) || !result.Terminated {
		t.Fatalf("result %#v error %v", result, err)
	}
}

func TestActivityOnlyUsesMatchingTurnAndKnownTypes(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 4), done: make(chan struct{})}
	f.events <- Message{Method: "item/started", Params: json.RawMessage(`{"threadId":"other","turnId":"turn","item":{"type":"commandExecution"}}`)}
	f.events <- Message{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"commandExecution","command":"secret"}}`)}
	f.events <- Message{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"unknown"}}`)}
	f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
	var seen []string
	_, err := RunControlled(context.Background(), f, "/tmp", "test", nil, nil, func(_ context.Context, phase, kind string, _ map[string]string) error {
		seen = append(seen, phase+":"+kind)
		return nil
	})
	if err != nil || len(seen) != 1 || seen[0] != "started:commandExecution" {
		t.Fatalf("activity %v: %v", seen, err)
	}
}

func TestSubagentActivityUsesOnlyMatchingTask(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 4), done: make(chan struct{})}
	f.events <- Message{Method: "item/started", Params: json.RawMessage(`{"threadId":"other","turnId":"turn","item":{"type":"collabAgentToolCall"}}`)}
	f.events <- Message{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"collabAgentToolCall","prompt":"private"}}`)}
	f.events <- Message{Method: "item/completed", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"type":"subAgentActivity","kind":"completed","agentThreadId":"child","text":"private"}}`)}
	f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
	var seen []string
	var metadata map[string]string
	_, err := RunControlled(context.Background(), f, "/tmp", "test", nil, nil, func(_ context.Context, phase, kind string, details map[string]string) error {
		seen = append(seen, phase+":"+kind)
		if kind == "subAgentActivity" {
			metadata = details
		}
		return nil
	})
	if err != nil || len(seen) != 2 || seen[0] != "started:collabAgentToolCall" || seen[1] != "completed:subAgentActivity" {
		t.Fatalf("activity %v: %v", seen, err)
	}
	if len(metadata) != 3 || metadata["agentThreadId"] != "child" || metadata["parentThreadId"] != "thread" || metadata["kind"] != "completed" {
		t.Fatalf("unexpected metadata: %v", metadata)
	}
}

type interruptRaceRPC struct{ *fakeRPC }

func (f interruptRaceRPC) Call(ctx context.Context, method string, params any, result any) error {
	if method == "turn/interrupt" {
		go func() {
			time.Sleep(20 * time.Millisecond)
			f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
		}()
		return errors.New("turn already finished")
	}
	return f.fakeRPC.Call(ctx, method, params, result)
}
func TestCompletionWinsInterruptError(t *testing.T) {
	f := interruptRaceRPC{&fakeRPC{events: make(chan Message, 2), done: make(chan struct{})}}
	stop := make(chan struct{})
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := RunControlled(ctx, f, "/tmp", "test", nil, stop, nil)
	if err != nil || !result.Terminated {
		t.Fatalf("completion lost to interrupt: result=%#v error=%v", result, err)
	}
}

func TestAppServerInterrupt(t *testing.T) {
	if os.Getenv("CODEX_SMOKE_TEST") != "1" {
		t.Skip("set CODEX_SMOKE_TEST=1 to use local Codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	stop := make(chan struct{})
	close(stop)
	execution, err := RunControlled(ctx, client, t.TempDir(), "Explain the design tradeoffs of a queue in detail. Do not use tools.", nil, stop, nil)
	if !errors.Is(err, ErrInterrupted) || !execution.Terminated {
		t.Fatalf("interrupt outcome: terminated=%v, error=%v", execution.Terminated, err)
	}
}

type approvalPolicyRPC struct{ RPC }

func (a approvalPolicyRPC) Call(ctx context.Context, method string, params any, result any) error {
	if method == "thread/start" {
		p, ok := params.(map[string]any)
		if !ok {
			return errors.New("invalid start parameters")
		}
		clone := make(map[string]any, len(p))
		for k, v := range p {
			clone[k] = v
		}
		clone["approvalPolicy"] = "untrusted"
		params = clone
	}
	return a.RPC.Call(ctx, method, params, result)
}
func TestAppServerApproval(t *testing.T) {
	if os.Getenv("CODEX_APPROVAL_SMOKE_TEST") != "1" {
		t.Skip("set CODEX_APPROVAL_SMOKE_TEST=1 to request a real approval")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	seen := false
	_, err = RunWithApprovals(ctx, approvalPolicyRPC{client}, t.TempDir(), "Use the shell tool to run exactly `printf 'hello'`; do not write files or use any other tool.", func(_ context.Context, event Message) error {
		seen = true
		return client.Reply(event.ID, map[string]string{"decision": "decline"})
	})
	if !seen {
		t.Fatalf("no approval request observed; run outcome: %v", err)
	}
	if err != nil && !errors.Is(err, ErrInterrupted) {
		t.Fatalf("turn after decline: %v", err)
	}
}

func TestFileApprovalIncludesMatchingChangeDetails(t *testing.T) {
	f := &fakeRPC{events: make(chan Message, 3), done: make(chan struct{})}
	f.events <- Message{Method: "item/started", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","item":{"id":"item-1","type":"fileChange","changes":[{"path":"a.txt","diff":"+hello","kind":{"type":"add"}}]}}`)}
	f.events <- Message{ID: json.RawMessage(`7`), Method: "item/fileChange/requestApproval", Params: json.RawMessage(`{"threadId":"thread","turnId":"turn","itemId":"item-1"}`)}
	received := make(chan bool, 1)
	_, err := RunWithApprovals(context.Background(), f, "/tmp", "test", func(_ context.Context, event Message) error {
		var params struct {
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		}
		err := json.Unmarshal(event.Params, &params)
		received <- err == nil && len(params.Changes) == 1 && params.Changes[0].Path == "a.txt"
		f.events <- Message{Method: "turn/completed", Params: json.RawMessage(`{"threadId":"thread","turn":{"id":"turn","status":"completed"}}`)}
		return nil
	})
	if err != nil || !<-received {
		t.Fatalf("file details were not forwarded: %v", err)
	}
}

func TestFailedTurnClassifiesKnownErrors(t *testing.T) {
	for _, tc := range []struct {
		message string
		want    error
	}{
		{"quota exceeded", ErrUsageLimit},
		{"authentication failed", ErrAuthentication},
		{"network connection failed", ErrNetwork},
		{"sandbox unavailable", ErrSandbox},
	} {
		t.Run(tc.message, func(t *testing.T) {
			f := &fakeRPC{events: make(chan Message, 1), done: make(chan struct{})}
			params, err := json.Marshal(map[string]any{"threadId": "thread", "turn": map[string]any{"id": "turn", "status": "failed", "error": map[string]string{"message": tc.message}}})
			if err != nil {
				t.Fatal(err)
			}
			f.events <- Message{Method: "turn/completed", Params: params}
			execution, err := Run(context.Background(), f, "/tmp", "test")
			if !errors.Is(err, tc.want) || !execution.Terminated {
				t.Fatalf("error=%v terminated=%v", err, execution.Terminated)
			}
		})
	}
}

type failingStartRPC struct {
	RPC
	method string
}

func (f failingStartRPC) Call(ctx context.Context, method string, params, result any) error {
	if method == f.method {
		return &RPCError{Message: "authentication failed: sensitive details"}
	}
	return f.RPC.Call(ctx, method, params, result)
}

func TestStartRPCErrorIsClassifiedWithoutExposingDetails(t *testing.T) {
	for _, method := range []string{"thread/start", "turn/start"} {
		t.Run(method, func(t *testing.T) {
			f := failingStartRPC{RPC: &fakeRPC{events: make(chan Message, 1), done: make(chan struct{})}, method: method}
			execution, err := Run(context.Background(), f, "/tmp", "test")
			if !errors.Is(err, ErrAuthentication) || execution.Terminated || strings.Contains(err.Error(), "sensitive details") {
				t.Fatalf("error=%v terminated=%v", err, execution.Terminated)
			}
		})
	}
}
