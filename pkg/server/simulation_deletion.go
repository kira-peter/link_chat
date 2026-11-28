package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"link-chat/pkg/protocol"
	"link-chat/pkg/storage"
)

type simulationDeletionStatus struct {
	Phase   string `json:"phase"`
	Total   int    `json:"total"`
	Deleted int    `json:"deleted"`
	Skipped int    `json:"skipped"`
	Failed  int    `json:"failed"`
}

type simulationDeletion struct {
	status simulationDeletionStatus // Guarded by simulationState.mu.
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *Server) apiDeleteSimulationUser(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req struct {
		KeepComments bool `json:"keep_comments"`
	}
	if err := decodeBody(w, r, &req); err != nil && !errors.Is(err, errNoBody) {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	s.writeAccountDeletion(w, s.deleteSimulationUser(r.Context(), a.actor(), r.PathValue("id"), req.KeepComments))
}

func (s *Server) deleteSimulationUser(ctx context.Context, actor Actor, id string, keepComments bool) error {
	s.accountWrites.Lock()
	defer s.accountWrites.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.simulation.enabled.Load() {
		return storage.ErrNotFound
	}
	target, err := s.st.UserByID(id)
	if err != nil {
		return err
	}
	return s.deleteAccount(actor, target, keepComments, true)
}

func (s *Server) apiStartSimulationDeletion(w http.ResponseWriter, r *http.Request, a adminSession) {
	var req struct {
		UserIDs      []string `json:"user_ids"`
		Milliseconds *int     `json:"interval_ms"`
		Seconds      int      `json:"interval_seconds"`
		KeepComments bool     `json:"keep_comments"`
	}
	// A full batch of generated IDs needs more room than the ordinary admin
	// request limit. Bound this endpoint by payload size and account count.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512<<10)).Decode(&req); err != nil || len(req.UserIDs) == 0 || len(req.UserIDs) > maxSimulationUsers {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	var ms int
	if req.Milliseconds != nil {
		ms = *req.Milliseconds
	} else if req.Seconds >= 1 && req.Seconds <= 3600 {
		ms = req.Seconds * 1000
	}
	if ms < 1 || ms > 3_600_000 {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	if !s.simulation.enabled.Load() {
		http.NotFound(w, r)
		return
	}
	select {
	case <-s.shuttingDown:
		writeErr(w, http.StatusServiceUnavailable, protocol.CodeShuttingDown)
		return
	default:
	}
	if job := s.simulation.deletion; job != nil && job.status.Phase != "finished" {
		writeErr(w, http.StatusConflict, protocol.CodeInvalidArgument)
		return
	}
	users, err := s.st.SimulationUsers()
	if err != nil {
		s.writeStorageErr(w, err)
		return
	}
	requested := make(map[string]bool, len(req.UserIDs))
	for _, id := range req.UserIDs {
		requested[id] = true
	}
	var ids []string
	for _, user := range users {
		if requested[user.ID] {
			ids = append(ids, user.ID)
		}
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, protocol.CodeInvalidArgument)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &simulationDeletion{status: simulationDeletionStatus{Phase: "running", Total: len(ids)}, cancel: cancel, done: make(chan struct{})}
	s.simulation.deletion = job
	go s.runSimulationDeletion(ctx, job, a.actor(), ids, time.Duration(ms)*time.Millisecond, req.KeepComments)
	writeJSON(w, http.StatusAccepted, job.status)
}

func (s *Server) apiStopSimulationDeletion(w http.ResponseWriter, _ *http.Request, _ adminSession) {
	s.stopSimulationDeletion()
	writeJSON(w, http.StatusOK, struct{}{})
}

func (s *Server) stopSimulationDeletion() <-chan struct{} {
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	return s.stopSimulationDeletionLocked()
}

func (s *Server) stopSimulationDeletionLocked() <-chan struct{} {
	job := s.simulation.deletion
	if job == nil {
		return nil
	}
	if job.status.Phase != "finished" {
		job.status.Phase = "stopping"
		job.cancel()
	}
	return job.done
}

func (s *Server) runSimulationDeletion(ctx context.Context, job *simulationDeletion, actor Actor, ids []string, interval time.Duration, keepComments bool) {
	defer job.cancel()
	defer func() {
		s.simulation.mu.Lock()
		job.status.Phase = "finished"
		close(job.done)
		s.simulation.mu.Unlock()
	}()
	for index, id := range ids {
		if index != 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		err := s.deleteSimulationUser(ctx, actor, id, keepComments)
		s.simulation.mu.Lock()
		switch {
		case err == nil:
			job.status.Deleted++
		case errors.Is(err, storage.ErrNotFound):
			job.status.Skipped++
		case !errors.Is(err, context.Canceled):
			job.status.Failed++
			s.log.Warn("simulation account deletion failed", "id", id, "err", err)
		}
		s.simulation.mu.Unlock()
	}
}
