package store

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

type ContainerExecution struct {
	TaskID      int64
	OwnerToken  string
	DaemonID    string
	ContainerID *string
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

// ReserveContainerExecution records the owner before any container is created.
func (s *Store) ReserveContainerExecution(ctx context.Context, taskID int64, ownerToken, daemonID string) error {
	if taskID < 1 || !validHex(ownerToken, 32) || len(daemonID) == 0 || len(daemonID) > 128 || strings.ContainsAny(daemonID, " \t\r\n") {
		return ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAntigravityTask(ctx, tx, taskID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `INSERT INTO container_executions(task_id,owner_token,daemon_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, taskID, ownerToken, daemonID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

// AttachContainerID records an immutable Docker ID after creation.
func (s *Store) AttachContainerID(ctx context.Context, taskID int64, ownerToken, containerID string) error {
	if taskID < 1 || !validHex(ownerToken, 32) || !validHex(containerID, 64) {
		return ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockAntigravityTask(ctx, tx, taskID); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `UPDATE container_executions SET container_id=$3 WHERE task_id=$1 AND owner_token=$2 AND container_id IS NULL`, taskID, ownerToken, containerID)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func lockAntigravityTask(ctx context.Context, tx pgx.Tx, taskID int64) error {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM tasks WHERE id=$1 AND provider='antigravity' AND status='running' FOR UPDATE`, taskID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return err
}

func (s *Store) ContainerExecution(ctx context.Context, taskID int64) (ContainerExecution, error) {
	var execution ContainerExecution
	err := s.Pool.QueryRow(ctx, `SELECT task_id,owner_token,daemon_id,container_id FROM container_executions WHERE task_id=$1`, taskID).Scan(&execution.TaskID, &execution.OwnerToken, &execution.DaemonID, &execution.ContainerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return execution, ErrNotFound
	}
	return execution, err
}

type BlockedContainerExecution struct {
	ProjectID int64
	ContainerExecution
}

func (s *Store) BlockedContainerExecutions(ctx context.Context) ([]BlockedContainerExecution, error) {
	rows, err := s.Pool.Query(ctx, `SELECT p.id,e.task_id,e.owner_token,e.daemon_id,e.container_id
 FROM projects p JOIN container_executions e ON e.task_id=p.blocked_task_id
 WHERE p.queue_state='blocked' AND p.blocked_provider='antigravity' ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var executions []BlockedContainerExecution
	for rows.Next() {
		var e BlockedContainerExecution
		if err = rows.Scan(&e.ProjectID, &e.TaskID, &e.OwnerToken, &e.DaemonID, &e.ContainerID); err != nil {
			return nil, err
		}
		executions = append(executions, e)
	}
	return executions, rows.Err()
}

// ReleaseContainerBlock requires the caller to have verified the container stopped.
func (s *Store) ReleaseContainerBlock(ctx context.Context, blocked BlockedContainerExecution, containerID string) error {
	if blocked.ProjectID < 1 || blocked.TaskID < 1 || !validHex(blocked.OwnerToken, 32) || !validHex(containerID, 64) || blocked.DaemonID == "" {
		return ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	seq, err := nextSequence(ctx, tx)
	if err != nil {
		return err
	}
	var recorded *string
	err = tx.QueryRow(ctx, `SELECT e.container_id FROM projects p JOIN container_executions e ON e.task_id=p.blocked_task_id
 WHERE p.id=$1 AND p.queue_state='blocked' AND p.blocked_provider='antigravity' AND e.task_id=$2 AND e.owner_token=$3 AND e.daemon_id=$4 FOR UPDATE OF p,e`, blocked.ProjectID, blocked.TaskID, blocked.OwnerToken, blocked.DaemonID).Scan(&recorded)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	if recorded != nil && *recorded != containerID {
		return ErrConflict
	}
	if recorded == nil {
		if _, err = tx.Exec(ctx, `UPDATE container_executions SET container_id=$2 WHERE task_id=$1`, blocked.TaskID, containerID); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE projects SET queue_state='ready',blocked_reason=NULL,blocked_host=NULL,blocked_boot=NULL,blocked_task_id=NULL WHERE id=$1`, blocked.ProjectID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'project',jsonb_build_object('queueState','ready'))`, seq, blocked.TaskID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
