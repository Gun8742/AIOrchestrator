package orchestrator

import (
	"context"
	"errors"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/codex"
	"pixel-agent-office/backend/internal/store"
)

// ExecuteAntigravity runs the isolated fixture path without mounting account credentials.
func ExecuteAntigravity(db *store.Store, daemonID, imageID string) Execute {
	return executeAntigravity(db, daemonID, imageID, antigravity.CreateContainer)
}

func AntigravityCleanup(db *store.Store) func(context.Context, int64) error {
	return func(ctx context.Context, taskID int64) error {
		execution, err := db.ContainerExecution(ctx, taskID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		if err != nil || execution.ContainerID == nil {
			return antigravity.ErrContainerUnconfirmed
		}
		return antigravity.RemoveStoppedContainer(ctx, antigravity.ContainerRef{ID: *execution.ContainerID, Owner: execution.OwnerToken, DaemonID: execution.DaemonID})
	}
}

func ExecuteAntigravityOAuth(db *store.Store, daemonID, imageID, volume string) Execute {
	create := func(ctx context.Context, owner, daemon, image, project string) (antigravity.ContainerRef, error) {
		return antigravity.CreateOAuthContainer(ctx, owner, daemon, image, project, volume)
	}
	return executeAntigravity(db, daemonID, imageID, create)
}

func executeAntigravity(db *store.Store, daemonID, imageID string, create func(context.Context, string, string, string, string) (antigravity.ContainerRef, error)) Execute {
	return func(ctx context.Context, taskID int64, project, prompt string, stop <-chan struct{}) (codex.Execution, error) {
		if err := ctx.Err(); err != nil {
			return codex.Execution{Terminated: true}, err
		}
		select {
		case <-stop:
			return codex.Execution{Terminated: true}, codex.ErrInterrupted
		default:
		}
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			select {
			case <-stop:
				cancel()
			case <-runCtx.Done():
			}
		}()
		ref, err := prepareContainer(runCtx, db, taskID, daemonID, imageID, project, create)
		if err != nil {
			return codex.Execution{}, err
		}
		result, err := antigravity.RunContainer(runCtx, ref, imageID, project, prompt)
		execution := codex.Execution{Result: result.Result, Terminated: result.Stopped}
		if errors.Is(err, context.Canceled) && ctx.Err() == nil && result.Stopped {
			return execution, codex.ErrInterrupted
		}
		return execution, err
	}
}
