package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var ErrProcess = errors.New("antigravity process failed")

const maxOutput = 8 << 20
const model = "gemini-3.8-flash-medium"

// Run starts the CLI but does not establish that descendants have stopped.
func Run(ctx context.Context, project, prompt string) (string, error) {
	expectedDir, err := filepath.EvalSymlinks(project)
	if err != nil {
		return "", ErrRuntimeMismatch
	}
	expectedDir, err = filepath.Abs(expectedDir)
	if err != nil {
		return "", ErrRuntimeMismatch
	}
	message, err := json.Marshal(map[string]any{"event": "user", "message": map[string]string{"content": prompt}})
	if err != nil {
		return "", ErrProcess
	}
	message = append(message, '\n')
	cmd := exec.CommandContext(ctx, "agy", "--model", model, "--sandbox", "--input-format", "stream-json", "--output-format", "stream-json")
	cmd.Dir = project
	configureProcess(cmd)
	cancelProcess := cmd.Cancel
	var cancelOnce sync.Once
	var cancelErr error
	cmd.Cancel = func() error {
		cancelOnce.Do(func() { cancelErr = cancelProcess() })
		return cancelErr
	}
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 3 * time.Second
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", ErrProcess
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", ErrProcess
	}
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrProcess
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
		actualDir, pathErr := filepath.EvalSymlinks(init.Init.Cwd)
		if pathErr != nil || !filepath.IsAbs(actualDir) || actualDir != expectedDir || init.Init.Model != model {
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
			_ = cmd.Cancel()
		}
	case <-ctx.Done():
		_ = cmd.Cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		result = <-done
		result.err = ctx.Err()
	}
	waitErr := cmd.Wait()
	if result.err != nil {
		return "", result.err
	}
	if waitErr != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrProcess
	}
	return result.result, nil
}
