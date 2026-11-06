package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"pixel-agent-office/backend/internal/antigravity"
	"pixel-agent-office/backend/internal/store"
)

// PrepareContainer requires the caller to block the project if creation is uncertain.
func PrepareContainer(ctx context.Context, db *store.Store, taskID int64, daemonID, imageID, project string) (antigravity.ContainerRef, error) {
	return prepareContainer(ctx, db, taskID, daemonID, imageID, project, antigravity.CreateContainer)
}

func prepareContainer(ctx context.Context, db *store.Store, taskID int64, daemonID, imageID, project string, create func(context.Context, string, string, string, string) (antigravity.ContainerRef, error)) (antigravity.ContainerRef, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return antigravity.ContainerRef{}, err
	}
	owner := hex.EncodeToString(token[:])
	if err := db.ReserveContainerExecution(ctx, taskID, owner, daemonID); err != nil {
		return antigravity.ContainerRef{}, err
	}
	ref, err := create(ctx, owner, daemonID, imageID, project)
	if err != nil {
		return antigravity.ContainerRef{}, err
	}
	if ref.Owner != owner || ref.DaemonID != daemonID {
		return antigravity.ContainerRef{}, antigravity.ErrContainerCreate
	}
	if err = db.AttachContainerID(ctx, taskID, owner, ref.ID); err != nil {
		return ref, err
	}
	return ref, nil
}
