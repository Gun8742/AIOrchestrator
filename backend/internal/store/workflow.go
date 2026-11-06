package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type WorkflowStepInput struct {
	Provider string `json:"provider"`
	Prompt   string `json:"prompt"`
}

type CreatedWorkflow struct {
	ID          int64 `json:"id"`
	FirstTaskID int64 `json:"firstTaskId"`
}

var ErrInvalidWorkflow = errors.New("invalid workflow")

// CreateWorkflow commits the first queued task together with the workflow.
func (s *Store) CreateWorkflow(ctx context.Context, projectID int64, steps []WorkflowStepInput) (CreatedWorkflow, error) {
	if projectID < 1 || len(steps) < 1 || len(steps) > 10 {
		return CreatedWorkflow{}, ErrInvalidWorkflow
	}
	for _, step := range steps {
		if (step.Provider != "codex" && step.Provider != "antigravity") || strings.TrimSpace(step.Prompt) == "" || len(step.Prompt) > 12000 {
			return CreatedWorkflow{}, ErrInvalidWorkflow
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return CreatedWorkflow{}, err
	}
	defer tx.Rollback(ctx)
	sequence, err := nextSequence(ctx, tx)
	if err != nil {
		return CreatedWorkflow{}, err
	}
	var created CreatedWorkflow
	err = tx.QueryRow(ctx, `INSERT INTO workflows(project_id,status)
 SELECT id,'running' FROM projects WHERE id=$1 RETURNING id`, projectID).Scan(&created.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return CreatedWorkflow{}, ErrNotFound
	}
	if err != nil {
		return CreatedWorkflow{}, err
	}
	for index, step := range steps {
		_, err = tx.Exec(ctx, `INSERT INTO workflow_steps(workflow_id,step_index,provider,prompt) VALUES($1,$2,$3,$4)`, created.ID, index, step.Provider, step.Prompt)
		if err != nil {
			return CreatedWorkflow{}, err
		}
	}
	err = tx.QueryRow(ctx, `INSERT INTO tasks(project_id,provider,prompt,status,queue_order)
 VALUES($1,$2,$3,'queued',$4) RETURNING id`, projectID, steps[0].Provider, steps[0].Prompt, sequence).Scan(&created.FirstTaskID)
	if err != nil {
		return CreatedWorkflow{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE workflow_steps SET task_id=$2 WHERE workflow_id=$1 AND step_index=0`, created.ID, created.FirstTaskID); err != nil {
		return CreatedWorkflow{}, err
	}
	if err = writeStatusEvent(ctx, tx, sequence, created.FirstTaskID, "queued"); err != nil {
		return CreatedWorkflow{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return CreatedWorkflow{}, err
	}
	return created, nil
}

func advanceWorkflow(ctx context.Context, tx pgx.Tx, taskID int64, status string, result *string, blocked bool) error {
	var workflowID int64
	var index int
	var workflowStatus string
	var cancelRequested bool
	err := tx.QueryRow(ctx, `SELECT w.id,s.step_index,w.status,w.cancel_requested
 FROM workflow_steps s JOIN workflows w ON w.id=s.workflow_id
 WHERE s.task_id=$1 FOR UPDATE OF w`, taskID).Scan(&workflowID, &index, &workflowStatus, &cancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if workflowStatus != "running" {
		return nil
	}
	if status == "failed" || blocked {
		_, err = tx.Exec(ctx, `UPDATE workflows SET status='failed',error='A step failed or its execution could not be confirmed',updated_at=now() WHERE id=$1`, workflowID)
		return err
	}
	if cancelRequested || status == "cancelled" {
		_, err = tx.Exec(ctx, `UPDATE workflows SET status='cancelled',updated_at=now() WHERE id=$1`, workflowID)
		return err
	}
	if status != "completed" || result == nil {
		_, err = tx.Exec(ctx, `UPDATE workflows SET status='failed',error='A step failed or its execution could not be confirmed',updated_at=now() WHERE id=$1`, workflowID)
		return err
	}
	var provider, prompt string
	err = tx.QueryRow(ctx, `SELECT provider,prompt FROM workflow_steps WHERE workflow_id=$1 AND step_index=$2`, workflowID, index+1).Scan(&provider, &prompt)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `UPDATE workflows SET status='completed',updated_at=now() WHERE id=$1`, workflowID)
		return err
	}
	if err != nil {
		return err
	}
	handoff := prompt + "\n\n--- Previous step result ---\n" + *result
	if len(handoff) > 12000 {
		_, err = tx.Exec(ctx, `UPDATE workflows SET status='failed',error='The next step input exceeds the supported size',updated_at=now() WHERE id=$1`, workflowID)
		return err
	}
	sequence, err := nextSequence(ctx, tx)
	if err != nil {
		return err
	}
	var nextTask int64
	err = tx.QueryRow(ctx, `INSERT INTO tasks(project_id,provider,prompt,status,queue_order)
 SELECT project_id,$2,$3,'queued',$4 FROM workflows WHERE id=$1 RETURNING id`, workflowID, provider, handoff, sequence).Scan(&nextTask)
	if err != nil {
		return err
	}
	updated, err := tx.Exec(ctx, `UPDATE workflow_steps SET task_id=$3 WHERE workflow_id=$1 AND step_index=$2 AND task_id IS NULL`, workflowID, index+1, nextTask)
	if err != nil {
		return err
	}
	if updated.RowsAffected() != 1 {
		return errors.New("workflow step already has a task")
	}
	if err = writeStatusEvent(ctx, tx, sequence, nextTask, "queued"); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE workflows SET updated_at=now() WHERE id=$1`, workflowID)
	return err
}

type WorkflowCancellation struct {
	Status       string `json:"status"`
	ActiveTaskID *int64 `json:"activeTaskId,omitempty"`
}

// CancelWorkflow persists the request before asking the runtime to stop.
func (s *Store) CancelWorkflow(ctx context.Context, workflowID int64) (WorkflowCancellation, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return WorkflowCancellation{}, err
	}
	defer tx.Rollback(ctx)
	sequence, err := nextSequence(ctx, tx)
	if err != nil {
		return WorkflowCancellation{}, err
	}
	var cancellation WorkflowCancellation
	var requested bool
	err = tx.QueryRow(ctx, `SELECT status,cancel_requested FROM workflows WHERE id=$1 FOR UPDATE`, workflowID).Scan(&cancellation.Status, &requested)
	if errors.Is(err, pgx.ErrNoRows) {
		return cancellation, ErrNotFound
	}
	if err != nil {
		return cancellation, err
	}
	if cancellation.Status != "running" {
		return cancellation, tx.Commit(ctx)
	}
	var taskID int64
	var taskStatus string
	err = tx.QueryRow(ctx, `SELECT t.id,t.status FROM workflow_steps s JOIN tasks t ON t.id=s.task_id
 WHERE s.workflow_id=$1 ORDER BY s.step_index DESC LIMIT 1 FOR UPDATE OF t`, workflowID).Scan(&taskID, &taskStatus)
	if err != nil {
		return cancellation, err
	}
	if taskStatus == "queued" {
		if _, err = tx.Exec(ctx, `UPDATE tasks SET status='cancelled',updated_at=now() WHERE id=$1 AND status='queued'`, taskID); err != nil {
			return cancellation, err
		}
		if _, err = tx.Exec(ctx, `UPDATE workflows SET status='cancelled',cancel_requested=true,updated_at=now() WHERE id=$1`, workflowID); err != nil {
			return cancellation, err
		}
		if err = writeStatusEvent(ctx, tx, sequence, taskID, "cancelled"); err != nil {
			return cancellation, err
		}
		cancellation.Status = "cancelled"
	} else if taskStatus == "running" || taskStatus == "waiting_for_user" {
		if !requested {
			if _, err = tx.Exec(ctx, `UPDATE workflows SET cancel_requested=true,updated_at=now() WHERE id=$1`, workflowID); err != nil {
				return cancellation, err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'workflow',jsonb_build_object('cancelRequested',true))`, sequence, taskID); err != nil {
				return cancellation, err
			}
		}
		cancellation.ActiveTaskID = &taskID
	} else {
		return cancellation, ErrConflict
	}
	if err = tx.Commit(ctx); err != nil {
		return WorkflowCancellation{}, err
	}
	return cancellation, nil
}

type WorkflowStep struct {
	Index    int     `json:"index"`
	Provider string  `json:"provider"`
	Prompt   string  `json:"prompt"`
	TaskID   *int64  `json:"taskId,omitempty"`
	Status   *string `json:"status,omitempty"`
	Result   *string `json:"result,omitempty"`
	Error    *string `json:"error,omitempty"`
}

type WorkflowSnapshot struct {
	ID              int64          `json:"id"`
	ProjectID       int64          `json:"projectId"`
	Status          string         `json:"status"`
	CancelRequested bool           `json:"cancelRequested"`
	Error           *string        `json:"error,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	Steps           []WorkflowStep `json:"steps"`
	Sequence        int64          `json:"sequence"`
}

func (s *Store) Workflow(ctx context.Context, id int64) (WorkflowSnapshot, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	out := WorkflowSnapshot{Steps: []WorkflowStep{}}
	if err = tx.QueryRow(ctx, `SELECT value FROM event_counter WHERE id=1`).Scan(&out.Sequence); err != nil {
		return out, err
	}
	err = tx.QueryRow(ctx, `SELECT id,project_id,status,cancel_requested,error,created_at,updated_at FROM workflows WHERE id=$1`, id).Scan(&out.ID, &out.ProjectID, &out.Status, &out.CancelRequested, &out.Error, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT s.step_index,s.provider,s.prompt,s.task_id,t.status,t.result,t.error FROM workflow_steps s LEFT JOIN tasks t ON t.id=s.task_id WHERE s.workflow_id=$1 ORDER BY s.step_index`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var step WorkflowStep
		if err = rows.Scan(&step.Index, &step.Provider, &step.Prompt, &step.TaskID, &step.Status, &step.Result, &step.Error); err != nil {
			rows.Close()
			return out, err
		}
		out.Steps = append(out.Steps, step)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

type WorkflowSummary struct {
	ID              int64     `json:"id"`
	ProjectID       int64     `json:"projectId"`
	Status          string    `json:"status"`
	CancelRequested bool      `json:"cancelRequested"`
	Error           *string   `json:"error,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type WorkflowList struct {
	Workflows []WorkflowSummary `json:"workflows"`
	Sequence  int64             `json:"sequence"`
}

func (s *Store) Workflows(ctx context.Context) (WorkflowList, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return WorkflowList{}, err
	}
	defer tx.Rollback(ctx)
	out := WorkflowList{Workflows: []WorkflowSummary{}}
	if err = tx.QueryRow(ctx, `SELECT value FROM event_counter WHERE id=1`).Scan(&out.Sequence); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id,project_id,status,cancel_requested,error,created_at,updated_at FROM workflows ORDER BY id DESC`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var w WorkflowSummary
		if err = rows.Scan(&w.ID, &w.ProjectID, &w.Status, &w.CancelRequested, &w.Error, &w.CreatedAt, &w.UpdatedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Workflows = append(out.Workflows, w)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
