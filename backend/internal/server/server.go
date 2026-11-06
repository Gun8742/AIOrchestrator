package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"pixel-agent-office/backend/internal/store"
)

type Projects interface {
	AddProject(context.Context, string) (store.Project, error)
	Projects(context.Context) ([]store.Project, error)
	CreateTask(context.Context, int64, string, string) (store.Task, error)
	Tasks(context.Context) (store.TaskSnapshot, error)
	Task(context.Context, int64) (store.DetailSnapshot, error)
	CancelQueued(context.Context, int64) (store.Task, error)
	CurrentSequence(context.Context) (int64, error)
	EventsAfter(context.Context, int64) ([]store.Event, error)
}
type ApprovalResponder interface {
	Respond(context.Context, int64, string, string) error
}

type CancelController interface{ RequestCancel(int64) bool }

type workflowStore interface {
	CreateWorkflow(context.Context, int64, []store.WorkflowStepInput) (store.CreatedWorkflow, error)
	Workflows(context.Context) (store.WorkflowList, error)
	Workflow(context.Context, int64) (store.WorkflowSnapshot, error)
	CancelWorkflow(context.Context, int64) (store.WorkflowCancellation, error)
}

type Server struct {
	codexAvailable       func() bool
	antigravityAvailable func() bool
	frontend             http.Handler
	canceller            CancelController
	approvals            ApprovalResponder
	projects             Projects
	host, origin         string
	mu                   sync.Mutex
	bootstrap            string
	expires              time.Time
	sessions             map[string]string
}

func New(projects Projects, host, bootstrap string, approvals ApprovalResponder, canceller CancelController) *Server {
	return &Server{codexAvailable: func() bool { _, err := exec.LookPath("codex"); return err == nil }, antigravityAvailable: func() bool { return false }, projects: projects, approvals: approvals, canceller: canceller, host: host, origin: "http://" + host, bootstrap: bootstrap, expires: time.Now().Add(10 * time.Minute), sessions: make(map[string]string)}
}

// SetFrontend configures the static application before serving requests.
func (s *Server) SetFrontend(handler http.Handler) { s.frontend = handler }

// SetAntigravityAvailable is called after the matching executor is registered, before serving.
func (s *Server) SetAntigravityAvailable(available bool) {
	s.antigravityAvailable = func() bool { return available }
}

func RandomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Host != s.host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != s.origin {
		http.Error(w, "invalid origin", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		if s.frontend == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		s.frontend.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/bootstrap" && r.Method == http.MethodPost {
		if origin != s.origin {
			http.Error(w, "origin required", http.StatusForbidden)
			return
		}
		s.bootstrapSession(w, r)
		return
	}
	csrf, ok := s.authorize(r)
	if !ok {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && (origin != s.origin || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1) {
		http.Error(w, "invalid request", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/api/session" && r.Method == http.MethodGet:
		writeJSON(w, 200, map[string]string{"csrfToken": csrf})
	case r.URL.Path == "/api/providers" && r.Method == http.MethodGet:
		s.listProviders(w, r)
	case r.URL.Path == "/api/projects" && r.Method == http.MethodGet:
		s.listProjects(w, r)
	case r.URL.Path == "/api/projects" && r.Method == http.MethodPost:
		s.addProject(w, r)
	case r.URL.Path == "/api/tasks" && r.Method == http.MethodGet:
		s.listTasks(w, r)
	case r.URL.Path == "/api/tasks" && r.Method == http.MethodPost:
		s.createTask(w, r)
	case r.URL.Path == "/api/workflows" && r.Method == http.MethodGet:
		s.listWorkflows(w, r)
	case r.URL.Path == "/api/workflows" && r.Method == http.MethodPost:
		s.createWorkflow(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/workflows/"):
		s.workflowRoute(w, r)
	case r.URL.Path == "/api/events" && r.Method == http.MethodGet:
		s.streamEvents(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/tasks/"):
		s.taskRoute(w, r)
	default:
		http.NotFound(w, r)
	}
}
func (s *Server) bootstrapSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Secret string `json:"secret"`
	}
	if err := decode(w, r, &body); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bootstrap == "" || time.Now().After(s.expires) || subtle.ConstantTimeCompare([]byte(body.Secret), []byte(s.bootstrap)) != 1 {
		http.Error(w, "invalid or expired secret", 403)
		return
	}
	session, err := RandomToken()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	csrf, err := RandomToken()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	s.bootstrap = ""
	s.sessions[session] = csrf
	http.SetCookie(w, &http.Cookie{Name: "pixel_session", Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, 200, map[string]string{"csrfToken": csrf})
}
func (s *Server) authorize(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("pixel_session")
	if err != nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	csrf, ok := s.sessions[cookie.Value]
	return csrf, ok
}
func (s *Server) providerAvailable(provider string) bool {
	switch provider {
	case "codex":
		return s.codexAvailable()
	case "antigravity":
		return s.antigravityAvailable()
	default:
		return false
	}
}

func (s *Server) listProviders(w http.ResponseWriter, _ *http.Request) {
	available := s.codexAvailable()
	reason := "Codex CLI installed; authentication is checked when a task runs"
	if !available {
		reason = "Codex CLI not found"
	}
	agyAvailable := s.antigravityAvailable()
	agyReason := "Docker image หรือ OAuth session ยังไม่พร้อม โปรดตั้งค่า Antigravity ก่อนเริ่ม Backend ใหม่"
	if agyAvailable {
		agyReason = "Docker OAuth พร้อมใช้; ตรวจสิทธิ์บัญชีเมื่อเริ่มงาน · interactive approval ไม่รองรับ · เครดิตเพิ่มปิดอยู่"
	}
	writeJSON(w, http.StatusOK, []map[string]any{
		{"id": "codex", "name": "Codex", "available": available, "reason": reason, "capabilities": map[string]bool{"cancellation": true, "approval": true}},
		{"id": "claude", "name": "Claude Code", "available": false, "reason": "ยังไม่ได้เปิดใช้งาน", "capabilities": map[string]bool{"cancellation": false, "approval": false}},
		{"id": "antigravity", "name": "Antigravity", "available": agyAvailable, "reason": agyReason, "capabilities": map[string]bool{"cancellation": agyAvailable, "approval": false}},
	})
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.projects.Projects(r.Context())
	if err != nil {
		http.Error(w, "cannot load projects", 500)
		return
	}
	writeJSON(w, 200, projects)
}
func (s *Server) addProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(w, r, &body); err != nil || strings.TrimSpace(body.Path) == "" {
		http.Error(w, "invalid path", 400)
		return
	}
	path, err := store.CanonicalDirectory(body.Path)
	if err != nil {
		http.Error(w, "directory not accessible", 400)
		return
	}
	project, err := s.projects.AddProject(r.Context(), path)
	if errors.Is(err, store.ErrDuplicate) {
		http.Error(w, "project already registered", 409)
		return
	}
	if err != nil {
		http.Error(w, "cannot add project", 500)
		return
	}
	writeJSON(w, 201, project)
}
func (s *Server) listTasks(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.projects.Tasks(r.Context())
	if err != nil {
		http.Error(w, "cannot load tasks", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID int64  `json:"projectId"`
		Prompt    string `json:"prompt"`
		Provider  string `json:"provider"`
	}
	if err := decode(w, r, &body); err != nil || body.ProjectID < 1 || strings.TrimSpace(body.Prompt) == "" || len(body.Prompt) > 12000 {
		http.Error(w, "invalid task", http.StatusBadRequest)
		return
	}
	if body.Provider == "" {
		body.Provider = "codex"
	}
	if !s.providerAvailable(body.Provider) {
		http.Error(w, "provider unavailable", http.StatusConflict)
		return
	}
	task, err := s.projects.CreateTask(r.Context(), body.ProjectID, body.Prompt, body.Provider)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "cannot create task", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) taskRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/tasks/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		detail, err := s.projects.Task(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "cannot load task", 500)
			return
		}
		writeJSON(w, 200, detail)
		return
	}
	if len(parts) == 3 && parts[1] == "approvals" && parts[2] != "" && r.Method == http.MethodPost {
		var body struct {
			Decision string `json:"decision"`
		}
		if err := decode(w, r, &body); err != nil || (body.Decision != "approve" && body.Decision != "deny") {
			http.Error(w, "invalid decision", 400)
			return
		}
		detail, err := s.projects.Task(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "cannot load task", 500)
			return
		}
		if detail.PendingApproval == nil || detail.PendingApproval.ID != parts[2] || s.approvals == nil {
			http.Error(w, "approval unavailable", 409)
			return
		}
		if err = s.approvals.Respond(r.Context(), id, parts[2], body.Decision); err != nil {
			if errors.Is(err, store.ErrConflict) {
				http.Error(w, "approval unavailable", 409)
				return
			}
			http.Error(w, "approval delivery failed", 500)
			return
		}
		detail, err = s.projects.Task(r.Context(), id)
		if err != nil {
			http.Error(w, "cannot load task", 500)
			return
		}
		writeJSON(w, 200, detail)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		task, err := s.projects.CancelQueued(r.Context(), id)
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, store.ErrActive) {
			if s.canceller != nil && s.canceller.RequestCancel(id) {
				detail, loadErr := s.projects.Task(r.Context(), id)
				if loadErr != nil {
					http.Error(w, "cannot load task", 500)
					return
				}
				writeJSON(w, 200, detail.Task)
				return
			}
			detail, loadErr := s.projects.Task(r.Context(), id)
			if loadErr != nil {
				http.Error(w, "cannot load task", 500)
				return
			}
			if detail.Task.Status == "completed" || detail.Task.Status == "failed" || detail.Task.Status == "cancelled" {
				writeJSON(w, 200, detail.Task)
				return
			}
			http.Error(w, "active cancellation unavailable", 409)
			return
		}
		if err != nil {
			http.Error(w, "cannot cancel task", 500)
			return
		}
		writeJSON(w, 200, task)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unavailable", 500)
		return
	}
	seq, err := s.projects.CurrentSequence(r.Context())
	if err != nil {
		http.Error(w, "stream unavailable", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, err = fmt.Fprintf(w, "event: ready\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
		events, err := s.projects.EventsAfter(r.Context(), seq)
		if err != nil {
			return
		}
		for _, event := range events {
			payload, err := json.Marshal(event)
			if err != nil {
				return
			}
			if _, err = fmt.Fprintf(w, "id: %d\nevent: task\ndata: %s\n\n", event.Sequence, payload); err != nil {
				return
			}
			seq = event.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid JSON")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
