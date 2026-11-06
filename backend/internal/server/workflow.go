package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"pixel-agent-office/backend/internal/store"
)

func (s *Server) listWorkflows(w http.ResponseWriter, r *http.Request) {
	db, ok := s.projects.(workflowStore)
	if !ok {
		http.Error(w, "workflows unavailable", http.StatusServiceUnavailable)
		return
	}
	result, err := db.Workflows(r.Context())
	if err != nil {
		http.Error(w, "cannot load workflows", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createWorkflow(w http.ResponseWriter, r *http.Request) {
	db, ok := s.projects.(workflowStore)
	if !ok {
		http.Error(w, "workflows unavailable", http.StatusServiceUnavailable)
		return
	}
	var request struct {
		ProjectID int64                     `json:"projectId"`
		Steps     []store.WorkflowStepInput `json:"steps"`
	}
	if err := decode(w, r, &request); err != nil || request.ProjectID < 1 || len(request.Steps) < 1 || len(request.Steps) > 10 {
		http.Error(w, "invalid workflow", http.StatusBadRequest)
		return
	}
	for _, step := range request.Steps {
		if !s.providerAvailable(step.Provider) {
			http.Error(w, "provider unavailable", http.StatusConflict)
			return
		}
		if strings.TrimSpace(step.Prompt) == "" || len(step.Prompt) > 12000 {
			http.Error(w, "invalid workflow", http.StatusBadRequest)
			return
		}
	}
	created, err := db.CreateWorkflow(r.Context(), request.ProjectID, request.Steps)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	if errors.Is(err, store.ErrInvalidWorkflow) {
		http.Error(w, "invalid workflow", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "cannot create workflow", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) workflowRoute(w http.ResponseWriter, r *http.Request) {
	db, ok := s.projects.(workflowStore)
	if !ok {
		http.Error(w, "workflows unavailable", http.StatusServiceUnavailable)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/workflows/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.writeWorkflow(w, r, db, id)
		return
	}
	if len(parts) != 2 || parts[1] != "cancel" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	cancelled, err := db.CancelWorkflow(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "cannot cancel workflow", http.StatusInternalServerError)
		return
	}
	if cancelled.ActiveTaskID != nil && (s.canceller == nil || !s.canceller.RequestCancel(*cancelled.ActiveTaskID)) {
		snapshot, loadErr := db.Workflow(r.Context(), id)
		if loadErr != nil {
			http.Error(w, "cannot load workflow", http.StatusInternalServerError)
			return
		}
		if snapshot.Status == "running" {
			http.Error(w, "cancellation saved; runtime unavailable", http.StatusConflict)
			return
		}
	}
	s.writeWorkflow(w, r, db, id)
}

func (s *Server) writeWorkflow(w http.ResponseWriter, r *http.Request, db workflowStore, id int64) {
	result, err := db.Workflow(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "cannot load workflow", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
