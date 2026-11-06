package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"time"
)

type ContainerResult struct {
	Result  string
	Stopped bool
}

// RunContainer requires the caller to have persisted the exact container ID before starting it.
func RunContainer(ctx context.Context, ref ContainerRef, imageID, project, prompt string) (ContainerResult, error) {
	if err := checkDaemon(ctx, ref.DaemonID); err != nil {
		return ContainerResult{}, err
	}
	if err := verifyCreatedContainer(ctx, ref, imageID, project); err != nil {
		return ContainerResult{}, err
	}
	message, err := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"content": prompt}})
	if err != nil {
		return ContainerResult{}, ErrProcess
	}
	message = append(message, '\n')
	cmd := exec.CommandContext(ctx, "docker", "start", "--attach", "--interactive", ref.ID)
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ContainerResult{}, ErrProcess
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ContainerResult{}, ErrProcess
	}
	if err = cmd.Start(); err != nil {
		return ContainerResult{}, ErrContainerUnconfirmed
	}
	type outcome struct {
		result string
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		reader := bufio.NewReaderSize(stdout, 64<<10)
		first, readErr := reader.ReadSlice('\n')
		if readErr != nil {
			done <- outcome{err: ErrIncomplete}
			return
		}
		var init streamEvent
		if json.Unmarshal(first, &init) != nil || init.Event != "init" {
			done <- outcome{err: ErrIncomplete}
			return
		}
		if init.Init.PermissionMode != "request-review" {
			done <- outcome{err: ErrPermissionMode}
			return
		}
		if init.Init.Cwd != "/workspace" || init.Init.Model != model {
			done <- outcome{err: ErrRuntimeMismatch}
			return
		}
		if _, writeErr := stdin.Write(message); writeErr != nil {
			done <- outcome{err: ErrProcess}
			return
		}
		if closeErr := stdin.Close(); closeErr != nil {
			done <- outcome{err: ErrProcess}
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(first), reader), maxOutput+1))
		if readErr != nil || len(data) > maxOutput {
			done <- outcome{err: ErrIncomplete}
			return
		}
		result, parseErr := ReadResult(bytes.NewReader(data))
		done <- outcome{result: result, err: parseErr}
	}()
	var result outcome
	select {
	case result = <-done:
		if result.err != nil {
			_ = stdin.Close()
			_ = cmd.Cancel()
		}
	case <-ctx.Done():
		_ = stdin.Close()
		_ = stdout.Close()
		_ = cmd.Cancel()
		result = <-done
		result.err = ctx.Err()
	}
	waitErr := cmd.Wait()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = StopContainer(cleanupCtx, ref); err != nil {
		return ContainerResult{}, ErrContainerUnconfirmed
	}
	if result.err != nil {
		return ContainerResult{Stopped: true}, result.err
	}
	if waitErr != nil {
		return ContainerResult{Stopped: true}, ErrProcess
	}
	return ContainerResult{Result: result.result, Stopped: true}, nil
}
