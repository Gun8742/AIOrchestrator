package orchestrator

import (
	"context"
	"errors"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/store"
)

func RecoverContainers(ctx context.Context, db *store.Store) error {
	return recoverContainers(ctx, db, antigravity.FindContainer, antigravity.StopContainer)
}

func recoverContainers(ctx context.Context, db *store.Store, find func(context.Context, string, string) (antigravity.ContainerRef, error), stop func(context.Context, antigravity.ContainerRef) error) error {
	blocked, err := db.BlockedContainerExecutions(ctx)
	if err != nil {
		return err
	}
	for _, execution := range blocked {
		if err = ctx.Err(); err != nil {
			return err
		}
		ref := antigravity.ContainerRef{Owner: execution.OwnerToken, DaemonID: execution.DaemonID}
		if execution.ContainerID == nil {
			ref, err = find(ctx, execution.OwnerToken, execution.DaemonID)
			if err != nil {
				continue
			}
		} else {
			ref.ID = *execution.ContainerID
		}
		if ref.Owner != execution.OwnerToken || ref.DaemonID != execution.DaemonID {
			continue
		}
		if err = stop(ctx, ref); err != nil {
			continue
		}
		if err = db.ReleaseContainerBlock(ctx, execution, ref.ID); err != nil && !errors.Is(err, store.ErrConflict) {
			return err
		}
	}
	return ctx.Err()
}
