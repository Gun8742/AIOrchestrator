package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDuplicate = errors.New("project already registered")

type Project struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Path          string    `json:"path"`
	QueueState    string    `json:"queueState"`
	BlockedReason *string   `json:"blockedReason,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type Store struct {
	Pool *pgxpool.Pool
	boot bootIdentity
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err = pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool, boot: currentBootIdentity()}, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS projects (
        id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
        name TEXT NOT NULL, path TEXT NOT NULL UNIQUE,
        queue_state TEXT NOT NULL DEFAULT 'ready' CHECK (queue_state IN ('ready','blocked')),
        blocked_reason TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
    );
 CREATE TABLE IF NOT EXISTS event_counter (id INTEGER PRIMARY KEY CHECK (id=1), value BIGINT NOT NULL);
 INSERT INTO event_counter(id,value) VALUES (1,0) ON CONFLICT DO NOTHING;
 CREATE TABLE IF NOT EXISTS tasks (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, project_id BIGINT NOT NULL REFERENCES projects(id),
 prompt TEXT NOT NULL, status TEXT NOT NULL CHECK (status IN ('queued','running','waiting_for_user','completed','failed','cancelled')),
 queue_order BIGINT UNIQUE NOT NULL, result TEXT, error TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS events (sequence BIGINT PRIMARY KEY, task_id BIGINT NOT NULL REFERENCES tasks(id), type TEXT NOT NULL, payload JSONB NOT NULL, timestamp TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS approvals (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), task_id BIGINT NOT NULL REFERENCES tasks(id), kind TEXT NOT NULL, details JSONB NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','resolved','expired')), decision TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now());
 CREATE UNIQUE INDEX IF NOT EXISTS one_pending_approval_per_task ON approvals(task_id) WHERE status = 'pending';
 ALTER TABLE tasks ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'codex';
 ALTER TABLE tasks ADD COLUMN IF NOT EXISTS execution_host TEXT;
 ALTER TABLE tasks ADD COLUMN IF NOT EXISTS execution_boot TEXT;
 ALTER TABLE projects ADD COLUMN IF NOT EXISTS blocked_host TEXT;
 ALTER TABLE projects ADD COLUMN IF NOT EXISTS blocked_boot TEXT;
 ALTER TABLE projects ADD COLUMN IF NOT EXISTS blocked_provider TEXT NOT NULL DEFAULT 'codex';
 ALTER TABLE projects ADD COLUMN IF NOT EXISTS blocked_task_id BIGINT REFERENCES tasks(id);
 CREATE TABLE IF NOT EXISTS workflows (
 id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 project_id BIGINT NOT NULL REFERENCES projects(id),
 status TEXT NOT NULL CHECK (status IN ('running','completed','failed','cancelled')),
 cancel_requested BOOLEAN NOT NULL DEFAULT false,
 error TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
 );
 CREATE TABLE IF NOT EXISTS container_executions (
 task_id BIGINT PRIMARY KEY REFERENCES tasks(id),
 owner_token TEXT NOT NULL UNIQUE,
 daemon_id TEXT NOT NULL,
 container_id TEXT UNIQUE
 );
 CREATE TABLE IF NOT EXISTS workflow_steps (
 workflow_id BIGINT NOT NULL REFERENCES workflows(id), step_index INTEGER NOT NULL CHECK (step_index >= 0),
 provider TEXT NOT NULL, prompt TEXT NOT NULL, task_id BIGINT UNIQUE REFERENCES tasks(id),
 PRIMARY KEY (workflow_id,step_index)
 );
 ALTER TABLE workflows ADD COLUMN IF NOT EXISTS error TEXT`)
	return err
}

func CanonicalDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("path must be a directory")
	}
	// Resolving the final dot requires search permission on the directory itself.
	directory, err := os.Open(canonical + string(os.PathSeparator) + ".")
	if err != nil {
		return "", err
	}
	if err = directory.Close(); err != nil {
		return "", err
	}
	return canonical, nil
}

func (s *Store) AddProject(ctx context.Context, path string) (Project, error) {
	var p Project
	err := s.Pool.QueryRow(ctx, `INSERT INTO projects(name,path) VALUES($1,$2)
        RETURNING id,name,path,queue_state,blocked_reason,created_at`, filepath.Base(path), path).
		Scan(&p.ID, &p.Name, &p.Path, &p.QueueState, &p.BlockedReason, &p.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Project{}, ErrDuplicate
	}
	return p, err
}

func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,path,queue_state,blocked_reason,created_at FROM projects ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := make([]Project, 0)
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.QueueState, &p.BlockedReason, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

// Task is the persisted user request. QueueOrder establishes FIFO independently of clocks.
type Task struct {
	ID         int64     `json:"id"`
	ProjectID  int64     `json:"projectId"`
	Provider   string    `json:"provider"`
	Prompt     string    `json:"prompt"`
	Status     string    `json:"status"`
	QueueOrder int64     `json:"-"`
	Result     *string   `json:"result,omitempty"`
	Error      *string   `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type Event struct {
	Sequence  int64           `json:"sequence"`
	TaskID    int64           `json:"taskId"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
}

type TaskSnapshot struct {
	Tasks    []Task `json:"tasks"`
	Sequence int64  `json:"sequence"`
}

var ErrNotFound = errors.New("not found")

func (s *Store) CreateTask(ctx context.Context, projectID int64, prompt, provider string) (Task, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)
	// Serialize the event counter until commit, so watermarks follow commit order.
	var seq int64
	if err = tx.QueryRow(ctx, `UPDATE event_counter SET value=value+1 WHERE id=1 RETURNING value`).Scan(&seq); err != nil {
		return Task{}, err
	}
	var t Task
	err = tx.QueryRow(ctx, `INSERT INTO tasks(project_id,prompt,status,queue_order,provider) SELECT id,$2,'queued',$3,$4 FROM projects WHERE id=$1
 RETURNING id,project_id,prompt,status,queue_order,result,error,created_at,updated_at,provider`, projectID, prompt, seq, provider).
		Scan(&t.ID, &t.ProjectID, &t.Prompt, &t.Status, &t.QueueOrder, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	payload, err := json.Marshal(map[string]any{"status": t.Status})
	if err != nil {
		return Task{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO events(sequence,task_id,type,payload) VALUES($1,$2,'status',$3)`, seq, t.ID, payload); err != nil {
		return Task{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return t, nil
}

func (s *Store) Tasks(ctx context.Context) (TaskSnapshot, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return TaskSnapshot{}, err
	}
	defer tx.Rollback(ctx)
	out := TaskSnapshot{Tasks: make([]Task, 0)}
	if err = tx.QueryRow(ctx, `SELECT value FROM event_counter WHERE id=1`).Scan(&out.Sequence); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT id,project_id,prompt,status,queue_order,result,error,created_at,updated_at,provider FROM tasks ORDER BY queue_order DESC`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var t Task
		if err = rows.Scan(&t.ID, &t.ProjectID, &t.Prompt, &t.Status, &t.QueueOrder, &t.Result, &t.Error, &t.CreatedAt, &t.UpdatedAt, &t.Provider); err != nil {
			rows.Close()
			return out, err
		}
		out.Tasks = append(out.Tasks, t)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}
