package orchestrator

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
)

type Queue interface {
	ClaimNextForProviders(context.Context, []string) (store.ClaimedTask, error)
	FinishTask(context.Context, int64, string, *string, *string, bool) error
}

type Execute func(context.Context, int64, string, string, <-chan struct{}) (codex.Execution, error)

type Worker struct {
	queue              Queue
	execute            Execute
	antigravity        Execute
	antigravityCleanup func(context.Context, int64) error
	slots              chan struct{}
	wg                 sync.WaitGroup
	mu                 sync.Mutex
	active             map[int64]*control
}

type control struct {
	stop      chan struct{}
	requested bool
}

func New(queue Queue, execute Execute) *Worker {
	return &Worker{queue: queue, execute: execute, slots: make(chan struct{}, 3), active: make(map[int64]*control)}
}

func NewWithAntigravity(queue Queue, codex, antigravity Execute) *Worker {
	worker := New(queue, codex)
	worker.antigravity = antigravity
	return worker
}

// SetAntigravityCleanup configures post-commit cleanup before Run starts.
func (w *Worker) SetAntigravityCleanup(cleanup func(context.Context, int64) error) {
	w.antigravityCleanup = cleanup
}

func (w *Worker) finishTask(task store.Task, status string, result, failure *string, blocked bool) error {
	err := w.queue.FinishTask(context.Background(), task.ID, status, result, failure, blocked)
	if err != nil || blocked || task.Provider != "antigravity" || w.antigravityCleanup == nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.antigravityCleanup(ctx, task.ID); err != nil {
		log.Print("Antigravity stopped-container cleanup deferred")
	}
	return nil
}

func (w *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	providers := []string{}
	if w.execute != nil {
		providers = append(providers, "codex")
	}
	if w.antigravity != nil {
		providers = append(providers, "antigravity")
	}
	failures := make(chan error, 3)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	defer func() { cancel(); w.wg.Wait() }()
	for {
		select {
		case <-ctx.Done():
			select {
			case err := <-failures:
				return err
			default:
			}
			return ctx.Err()
		case err := <-failures:
			return err
		case <-ticker.C:
			select {
			case w.slots <- struct{}{}:
			default:
				continue
			}
			w.mu.Lock()
			claimed, err := w.queue.ClaimNextForProviders(ctx, providers)
			if err != nil {
				w.mu.Unlock()
				<-w.slots
				if errors.Is(err, store.ErrNotFound) {
					continue
				}
				return err
			}
			ctrl := &control{stop: make(chan struct{})}
			w.active[claimed.Task.ID] = ctrl
			w.mu.Unlock()
			w.wg.Add(1)
			go func() {
				defer w.wg.Done()
				defer func() { <-w.slots }()
				if err := w.runTask(ctx, claimed, ctrl); err != nil {
					failures <- err
					cancel()
				}
				w.mu.Lock()
				delete(w.active, claimed.Task.ID)
				w.mu.Unlock()
			}()
		}
	}
}
func (w *Worker) RequestCancel(taskID int64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	ctrl, ok := w.active[taskID]
	if !ok {
		return false
	}
	if !ctrl.requested {
		ctrl.requested = true
		close(ctrl.stop)
	}
	return true
}
func (w *Worker) runTask(ctx context.Context, claimed store.ClaimedTask, ctrl *control) error {
	path, err := store.CanonicalDirectory(claimed.ProjectPath)
	if err != nil || path != claimed.ProjectPath {
		msg := "Project directory is unavailable or changed"
		return w.finishTask(claimed.Task, "failed", nil, &msg, false)
	}
	execute := w.execute
	if claimed.Task.Provider == "antigravity" {
		execute = w.antigravity
	} else if claimed.Task.Provider != "" && claimed.Task.Provider != "codex" {
		execute = nil
	}
	if execute == nil {
		message := "Agent provider is unavailable"
		return w.finishTask(claimed.Task, "failed", nil, &message, false)
	}
	execution, err := execute(ctx, claimed.Task.ID, path, claimed.Task.Prompt, ctrl.stop)
	if err == nil && !execution.Terminated {
		err = errors.New("execution termination could not be confirmed")
	}
	status := "completed"
	var result, errText *string
	if err != nil {
		w.mu.Lock()
		requested := ctrl.requested
		w.mu.Unlock()
		if requested && errors.Is(err, codex.ErrInterrupted) && execution.Terminated {
			return w.finishTask(claimed.Task, "cancelled", nil, nil, false)
		}
		status = "failed"
		message := "Codex execution failed; check the local runtime configuration"
		if claimed.Task.Provider == "antigravity" {
			message = "Antigravity execution failed; check the local runtime configuration"
			if errors.Is(err, antigravity.ErrPermissionDenied) {
				message = "Antigravity denied a requested action; review its permissions"
			}
			if errors.Is(err, antigravity.ErrContainerUnconfirmed) {
				message = "Antigravity container could not be confirmed stopped; project queue remains blocked"
			}
		}
		if errors.Is(err, ErrApprovalTimeout) {
			message = "Codex approval timed out; check the task and retry after confirming the project is safe"
		} else if errors.Is(err, codex.ErrApprovalUnavailable) {
			message = "Codex requested an unsupported approval type"
		} else if errors.Is(err, codex.ErrModelUnavailable) {
			message = "Configured Codex model is unavailable; check PIXEL_CODEX_MODEL and account access"
		} else if errors.Is(err, codex.ErrUsageLimit) {
			message = "Codex usage limit reached; check account usage and retry later"
		} else if errors.Is(err, codex.ErrAuthentication) {
			message = "Codex authentication or account access failed; check CLI sign-in and access"
		} else if errors.Is(err, codex.ErrNetwork) {
			message = "Codex network or stream failed; check connectivity before retrying"
		} else if errors.Is(err, codex.ErrSandbox) {
			message = "Codex sandbox failed; check the local runtime configuration"
		}
		errText = &message
	} else {
		sanitized := execution.Result
		for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "GEMINI_API_KEY"} {
			if secret := os.Getenv(key); secret != "" {
				sanitized = strings.ReplaceAll(sanitized, secret, "[REDACTED]")
			}
		}
		result = &sanitized
	}
	// Unknown execution state must keep the project blocked until it is verified stopped.
	return w.finishTask(claimed.Task, status, result, errText, !execution.Terminated)
}

type ActivityStore interface {
	AppendActivity(context.Context, int64, string, string, map[string]string) error
}

func ExecuteCodex(approvals *Approvals, activities ActivityStore) Execute {
	return func(ctx context.Context, taskID int64, path, prompt string, stop <-chan struct{}) (codex.Execution, error) {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		startupFinished := make(chan struct{})
		watchDone := make(chan struct{})
		go func() {
			defer close(watchDone)
			select {
			case <-stop:
				cancel()
			case <-startupFinished:
			case <-runCtx.Done():
			}
		}()
		client, err := codex.Start(runCtx)
		close(startupFinished)
		<-watchDone
		if err != nil {
			if ctx.Err() != nil {
				return codex.Execution{Terminated: true}, ctx.Err()
			}
			select {
			case <-stop:
				return codex.Execution{Terminated: true}, codex.ErrInterrupted
			default:
				return codex.Execution{Terminated: true}, errors.New("Cannot start Codex App Server")
			}
		}
		defer client.Close()
		if runCtx.Err() != nil {
			if ctx.Err() != nil {
				return codex.Execution{Terminated: true}, ctx.Err()
			}
			return codex.Execution{Terminated: true}, codex.ErrInterrupted
		}
		go func() {
			select {
			case <-client.Done():
				cancel()
			case <-runCtx.Done():
			}
		}()
		return codex.RunControlled(runCtx, client, path, prompt, func(ctx context.Context, event codex.Message) error {
			return approvals.Handle(ctx, taskID, client, event)
		}, stop, func(ctx context.Context, phase, itemType string, metadata map[string]string) error {
			return activities.AppendActivity(ctx, taskID, phase, itemType, metadata)
		})
	}

}
