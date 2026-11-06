package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type Approval struct {
	ID        string          `json:"id"`
	TaskID    int64           `json:"taskId"`
	Kind      string          `json:"kind"`
	Details   json.RawMessage `json:"details"`
	Status    string          `json:"status"`
	Decision  *string         `json:"decision,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

var ErrConflict = errors.New("conflicting state")

func (s *Store) BeginApproval(ctx context.Context, taskID int64, kind string, details json.RawMessage) (Approval, error) {
	if !json.Valid(details) {
		return Approval{}, errors.New("invalid approval details")
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Approval{}, err
	}
	defer tx.Rollback(ctx)
	seq, err := nextSequence(ctx, tx)
	if err != nil {
		return Approval{}, err
	}
	var task int64
	err = tx.QueryRow(ctx, `UPDATE tasks SET status='waiting_for_user',updated_at=now() WHERE id=$1 AND status='running' RETURNING id`, taskID).Scan(&task)
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrConflict
	}
	if err != nil {
		return Approval{}, err
	}
	var a Approval
	err = tx.QueryRow(ctx, `INSERT INTO approvals(task_id,kind,details,status) VALUES($1,$2,$3,'pending') RETURNING id::text,task_id,kind,details,status,decision,created_at`, taskID, kind, details).Scan(&a.ID, &a.TaskID, &a.Kind, &a.Details, &a.Status, &a.Decision, &a.CreatedAt)
	if err != nil {
		return Approval{}, err
	}
	payload, err := json.Marshal(map[string]any{"status": "waiting_for_user", "approval": a})
	if err != nil {
		return Approval{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'approval',$3)`, seq, taskID, payload); err != nil {
		return Approval{}, err
	}
	return a, tx.Commit(ctx)
}

func (s *Store) ResolveApproval(ctx context.Context, taskID int64, id, decision string) error {
	if decision != "approve" && decision != "deny" {
		return errors.New("invalid decision")
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
	var found int64
	err = tx.QueryRow(ctx, `UPDATE approvals SET status='resolved',decision=$3 WHERE id::text=$1 AND task_id=$2 AND status='pending' RETURNING task_id`, id, taskID, decision).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `UPDATE tasks SET status='running',updated_at=now() WHERE id=$1 AND status='waiting_for_user' RETURNING id`, taskID).Scan(&found)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"status": "running", "approvalId": id, "decision": decision})
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'approval',$3)`, seq, taskID, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) PendingApproval(ctx context.Context, taskID int64) (*Approval, error) {
	var a Approval
	err := s.Pool.QueryRow(ctx, `SELECT id::text,task_id,kind,details,status,decision,created_at FROM approvals WHERE task_id=$1 AND status='pending'`, taskID).Scan(&a.ID, &a.TaskID, &a.Kind, &a.Details, &a.Status, &a.Decision, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
