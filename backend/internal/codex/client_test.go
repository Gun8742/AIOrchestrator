package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

func TestClientRoutesResponsesAndRequests(t *testing.T) {
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	c := newClient(clientIn, clientOut)
	defer c.Close()
	defer serverIn.Close()
	defer serverOut.Close()
	go func() {
		scanner := bufio.NewScanner(serverIn)
		if !scanner.Scan() {
			return
		}
		var request Message
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		_, _ = serverOut.Write([]byte(`{"id":17,"method":"item/commandExecution/requestApproval","params":{"command":"test"}}` + "\n"))
		_, _ = serverOut.Write([]byte(`{"id":` + string(request.ID) + `,"result":{"value":"ok"}}` + "\n"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result struct {
		Value string `json:"value"`
	}
	if err := c.Call(ctx, "test", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Value != "ok" {
		t.Fatalf("result: %#v", result)
	}
	select {
	case event := <-c.Events():
		if event.Method != "item/commandExecution/requestApproval" || string(event.ID) != "17" {
			t.Fatalf("event: %#v", event)
		}
	case <-ctx.Done():
		t.Fatal("missing server request")
	}
}

func TestAppServerHandshake(t *testing.T) {
	if os.Getenv("CODEX_SMOKE_TEST") != "1" {
		t.Skip("set CODEX_SMOKE_TEST=1 to use local Codex")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.Close()
}
