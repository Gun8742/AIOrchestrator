package store

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
)

type ClaimedTask struct {
	Task        Task
	ProjectPath string
}

func nextSequence(ctx context.Context, tx pgx.Tx) (int64, error) {
	var seq int64
	err := tx.QueryRow(ctx, `UPDATE event_counter SET value=value+1 WHERE id=1 RETURNING value`).Scan(&seq)
	return seq, err
}
func writeStatusEvent(ctx context.Context, tx pgx.Tx, seq, id int64, status string) error {
	payload, _ := json.Marshal(map[string]string{"status": status})
	_, err := tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'status',$3)`, seq, id, payload)
	return err
}

func writeOutcomeEvent(ctx context.Context, tx pgx.Tx, taskID int64, kind, value string) error {
	seq, err := nextSequence(ctx, tx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{kind: value})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,$3,$4)`, seq, taskID, kind, payload)
	return err
}

func (s *Store) ClaimNext(ctx context.Context) (ClaimedTask, error) {
	return s.ClaimNextForProviders(ctx, []string{"codex"})
}

func (s *Store) ClaimNextForProviders(ctx context.Context, providers []string) (ClaimedTask, error) {
	if len(providers) == 0 {
		return ClaimedTask{}, ErrNotFound
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return ClaimedTask{}, err
	}
	defer tx.Rollback(ctx)
	seq, err := nextSequence(ctx, tx)
	if err != nil {
		return ClaimedTask{}, err
	}
	var c ClaimedTask
	t := &c.Task
	err = tx.QueryRow(ctx, `SELECT t.id,t.project_id,t.prompt,t.status,t.queue_order,t.result,t.error,t.created_at,t.updated_at,p.path,t.provider
 FROM tasks t JOIN projects p ON p.id=t.project_id
 WHERE t.status='queued' AND t.provider=ANY($1::text[]) AND p.queue_state='ready' AND NOT EXISTS
 (SELECT 1 FROM tasks older WHERE older.project_id=t.project_id AND older.status='queued' AND older.queue_order<t.queue_order) AND NOT EXISTS
 (SELECT 1 FROM tasks active WHERE active.project_id=t.project_id AND active.status IN ('running','waiting_for_user'))
 ORDER BY t.queue_order LIMIT 1 FOR UPDATE OF t`, providers).Scan(&t.ID, &t.ProjectID, &t.Prompt, &t.Status, &t.QueueOrder, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt, &c.ProjectPath, &t.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	err = tx.QueryRow(ctx, `UPDATE tasks SET status='running',updated_at=now(),execution_host=NULLIF($2,''),execution_boot=NULLIF($3,'') WHERE id=$1 RETURNING status,updated_at`, t.ID, s.boot.host, s.boot.boot).Scan(&t.Status, &t.UpdatedAt)
	if err != nil {
		return c, err
	}
	if err = writeStatusEvent(ctx, tx, seq, t.ID, t.Status); err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}

func (s *Store) FinishTask(ctx context.Context, id int64, status string, result, errorText *string, block bool) error {
	if status != "completed" && status != "failed" && status != "cancelled" {
		return errors.New("invalid terminal status")
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
	var projectID int64
	var provider string
	var executionHost, executionBoot *string
	err = tx.QueryRow(ctx, `UPDATE tasks SET status=$2,result=$3,error=$4,updated_at=now() WHERE id=$1 AND status IN ('running','waiting_for_user') RETURNING project_id,execution_host,execution_boot,provider`, id, status, result, errorText).Scan(&projectID, &executionHost, &executionBoot, &provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE approvals SET status='expired' WHERE task_id=$1 AND status='pending'`, id); err != nil {
		return err
	}
	if block {
		if _, err = tx.Exec(ctx, `UPDATE projects SET queue_state='blocked',blocked_reason='Cannot confirm that the previous execution stopped',blocked_host=$2,blocked_boot=$3,blocked_provider=$4,blocked_task_id=$5 WHERE id=$1`, projectID, executionHost, executionBoot, provider, id); err != nil {
			return err
		}
	}
	if err = writeStatusEvent(ctx, tx, seq, id, status); err != nil {
		return err
	}
	if status == "completed" && result != nil {
		if err = writeOutcomeEvent(ctx, tx, id, "result", *result); err != nil {
			return err
		}
	} else if status == "failed" && errorText != nil {
		if err = writeOutcomeEvent(ctx, tx, id, "error", *errorText); err != nil {
			return err
		}
	}
	if err = advanceWorkflow(ctx, tx, id, status, result, block); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) RecoverInterrupted(ctx context.Context) error {
	rows, err := s.Pool.Query(ctx, `SELECT id FROM tasks WHERE status IN ('running','waiting_for_user') ORDER BY queue_order`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	reason := "Service stopped while task was running; verify that its process has stopped"
	for _, id := range ids {
		if err = s.FinishTask(ctx, id, "failed", nil, &reason, true); err != nil {
			return err
		}
	}
	if s.boot.host != "" && s.boot.boot != "" {
		_, err = s.Pool.Exec(ctx, `UPDATE projects SET queue_state='ready',blocked_reason=NULL,blocked_host=NULL,blocked_boot=NULL,blocked_task_id=NULL WHERE queue_state='blocked' AND blocked_provider='codex' AND blocked_host=$1 AND blocked_boot IS NOT NULL AND blocked_boot<>$2`, s.boot.host, s.boot.boot)
		if err != nil {
			return err
		}
	}
	return nil
}
