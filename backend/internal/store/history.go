package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
)

type DetailSnapshot struct {
	Task            Task      `json:"task"`
	Events          []Event   `json:"events"`
	Sequence        int64     `json:"sequence"`
	PendingApproval *Approval `json:"pendingApproval,omitempty"`
}

func (s *Store) Task(ctx context.Context, id int64) (DetailSnapshot, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return DetailSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	out := DetailSnapshot{Events: []Event{}}
	if err = tx.QueryRow(ctx, `SELECT value FROM event_counter WHERE id=1`).Scan(&out.Sequence); err != nil {
		return out, err
	}
	t := &out.Task
	err = tx.QueryRow(ctx, `SELECT id,project_id,prompt,status,queue_order,result,error,created_at,updated_at,provider FROM tasks WHERE id=$1`, id).Scan(&t.ID, &t.ProjectID, &t.Prompt, &t.Status, &t.QueueOrder, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	var approval Approval
	err = tx.QueryRow(ctx, `SELECT id::text,task_id,kind,details,status,decision,created_at FROM approvals WHERE task_id=$1 AND status='pending'`, id).Scan(&approval.ID, &approval.TaskID, &approval.Kind, &approval.Details, &approval.Status, &approval.Decision, &approval.CreatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if err == nil {
		out.PendingApproval = &approval
	}
	rows, err := tx.Query(ctx, `SELECT sequence,task_id,type,payload,timestamp FROM events WHERE task_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.Sequence, &e.TaskID, &e.Type, &e.Payload, &e.Timestamp); err != nil {
			rows.Close()
			return out, err
		}
		out.Events = append(out.Events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *Store) CurrentSequence(ctx context.Context) (int64, error) {
	var seq int64
	err := s.Pool.QueryRow(ctx, `SELECT value FROM event_counter WHERE id=1`).Scan(&seq)
	return seq, err
}

func (s *Store) EventsAfter(ctx context.Context, seq int64) ([]Event, error) {
	rows, err := s.Pool.Query(ctx, `SELECT sequence,task_id,type,payload,timestamp FROM events WHERE sequence>$1 ORDER BY sequence LIMIT 100`, seq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.Sequence, &e.TaskID, &e.Type, &e.Payload, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

var ErrActive = errors.New("task is active")

func (s *Store) CancelQueued(ctx context.Context, id int64) (Task, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	var seq int64
	if err = tx.QueryRow(ctx, `UPDATE event_counter SET value=value+1 WHERE id=1 RETURNING value`).Scan(&seq); err != nil {
		return Task{}, err
	}
	var t Task
	err = tx.QueryRow(ctx, `SELECT id,project_id,prompt,status,queue_order,result,error,created_at,updated_at,provider FROM tasks WHERE id=$1 FOR UPDATE`, id).Scan(&t.ID, &t.ProjectID, &t.Prompt, &t.Status, &t.QueueOrder, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	if t.Status == "running" || t.Status == "waiting_for_user" {
		return Task{}, ErrActive
	}
	if t.Status != "queued" {
		return t, nil
	}
	err = tx.QueryRow(ctx, `UPDATE tasks SET status='cancelled',updated_at=now() WHERE id=$1 RETURNING status,updated_at`, id).Scan(&t.Status, &t.UpdatedAt)
	if err != nil {
		return Task{}, err
	}
	payload, _ := json.Marshal(map[string]string{"status": t.Status})
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'status',$3)`, seq, id, payload); err != nil {
		return Task{}, err
	}
	if err = advanceWorkflow(ctx, tx, id, "cancelled", nil, false); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return t, nil
}

func (s *Store) AppendActivity(ctx context.Context, taskID int64, phase, itemType string, metadata map[string]string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	seq, err := nextSequence(ctx, tx)
	if err != nil {
		return err
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT id FROM tasks WHERE id=$1 AND status IN ('running','waiting_for_user')`, taskID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return err
	}
	fields := map[string]string{"phase": phase, "itemType": itemType}
	for _, key := range []string{"agentThreadId", "parentThreadId", "kind"} {
		if value := metadata[key]; value != "" {
			for _, secretKey := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "GEMINI_API_KEY"} {
				if secret := os.Getenv(secretKey); secret != "" {
					value = strings.ReplaceAll(value, secret, "[REDACTED]")
				}
			}
			fields[key] = value
		}
	}
	payload, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'activity',$3)`, seq, id, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
