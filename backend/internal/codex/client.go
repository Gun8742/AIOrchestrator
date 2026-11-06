package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

type Message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

type Client struct {
	writer  io.WriteCloser
	command *exec.Cmd
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int64
	pending map[int64]chan Message
	events  chan Message
	done    chan struct{}
	once    sync.Once
}

func Start(ctx context.Context) (*Client, error) {
	args := []string{"app-server", "--listen", "stdio://"}
	if model := os.Getenv("PIXEL_CODEX_MODEL"); model != "" {
		args = append(args, "-c", "model="+strconv.Quote(model))
	}
	cmd := exec.CommandContext(ctx, "codex", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	c := newClient(stdout, stdin)
	c.command = cmd
	var result json.RawMessage
	if err = c.Call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "pixel_agent_office", "version": "0.1.0"}}, &result); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err = c.Notify("initialized", nil); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func newClient(reader io.Reader, writer io.WriteCloser) *Client {
	c := &Client{writer: writer, pending: make(map[int64]chan Message), events: make(chan Message, 128), done: make(chan struct{})}
	go c.read(reader)
	return c
}

func (c *Client) read(reader io.Reader) {
	defer c.once.Do(func() { close(c.done) })
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var msg Message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			return
		}
		if len(msg.ID) > 0 && msg.Method == "" {
			var id int64
			if json.Unmarshal(msg.ID, &id) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
			}
			continue
		}
		select {
		case c.events <- msg:
		case <-c.done:
			return
		}
	}
}

func (c *Client) send(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.writer.Write(data)
	return err
}

func (c *Client) Call(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	c.next++
	id := c.next
	ch := make(chan Message, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if err := c.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errors.New("codex app server disconnected")
	case msg := <-ch:
		if msg.Error != nil {
			return msg.Error
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(msg.Result, result); err != nil {
			return fmt.Errorf("decode %s: %w", method, err)
		}
		return nil
	}
}
func (c *Client) Notify(method string, params any) error {
	return c.send(map[string]any{"method": method, "params": params})
}
func (c *Client) Reply(id json.RawMessage, result any) error {
	return c.send(map[string]any{"id": id, "result": result})
}
func (c *Client) Events() <-chan Message { return c.events }
func (c *Client) Done() <-chan struct{}  { return c.done }
func (c *Client) Close() error {
	c.once.Do(func() { close(c.done) })
	_ = c.writer.Close()
	if c.command != nil {
		if c.command.Process != nil {
			_ = c.command.Process.Kill()
		}
		return c.command.Wait()
	}
	return nil
}
