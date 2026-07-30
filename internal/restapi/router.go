package restapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-ai-executor/internal/humanauth"
	"go-ai-executor/internal/sandbox"
	"go-ai-executor/internal/storage"
)

type RouterOptions struct {
	DB          *gorm.DB
	Manager     *sandbox.Manager
	AuthProvider humanauth.Provider
}

func NewRouter(opts RouterOptions) http.Handler {
	r := chi.NewRouter()

	// Require human session for all REST API endpoints
	r.Use(func(next http.Handler) http.Handler {
		return humanauth.RequireHumanSession(opts.AuthProvider, next)
	})

	// /api/me
	r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
		identity, ok := humanauth.HumanIdentityFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		writeJSON(w, http.StatusOK, identity)
	})

	// /api/agents
	r.Get("/agents", func(w http.ResponseWriter, r *http.Request) {
		agents, err := storage.ListAgents(opts.DB)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		type AgentWithCreds struct {
			storage.Actor
			Credentials []storage.AgentCredential `json:"credentials"`
		}

		var result []AgentWithCreds
		for _, agent := range agents {
			creds, _ := storage.ListAgentCredentials(opts.DB, agent.ID)
			result = append(result, AgentWithCreds{
				Actor:       agent,
				Credentials: creds,
			})
		}

		writeJSON(w, http.StatusOK, result)
	})

	r.Post("/agents", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DisplayName string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DisplayName == "" {
			writeError(w, http.StatusBadRequest, "display_name is required")
			return
		}

		agent, err := storage.CreateAgent(opts.DB, req.DisplayName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, agent)
	})

	r.Post("/agents/{id}/tokens", func(w http.ResponseWriter, r *http.Request) {
		agentID := chi.URLParam(r, "id")
		rawToken, cred, err := storage.IssueAgentToken(opts.DB, agentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"raw_token":  rawToken,
			"credential": cred,
		})
	})

	r.Delete("/agents/{id}/tokens/{credId}", func(w http.ResponseWriter, r *http.Request) {
		credID := chi.URLParam(r, "credId")
		if err := storage.RevokeAgentToken(opts.DB, credID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
	})

	r.Get("/agents/{id}/logs", func(w http.ResponseWriter, r *http.Request) {
		agentID := chi.URLParam(r, "id")
		logs, err := storage.ListExecLogs(opts.DB, agentID, 100)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, logs)
	})

	// Server-Sent Events (SSE) Live Terminal Output Stream
	r.Get("/agents/{id}/stream", func(w http.ResponseWriter, r *http.Request) {
		agentID := chi.URLParam(r, "id")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		broadcaster := opts.Manager.GetBroadcaster()
		ch, unsubscribe := broadcaster.Subscribe(agentID)
		defer unsubscribe()

		// Send initial keep-alive comment
		fmt.Fprintf(w, ": connected to agent %s terminal stream\n\n", agentID)
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-ch:
				if !ok {
					return
				}
				data, err := json.Marshal(event)
				if err == nil {
					fmt.Fprintf(w, "data: %s\n\n", data)
					flusher.Flush()
				}
			}
		}
	})

	// Web UI manual command trigger
	r.Post("/agents/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		agentID := chi.URLParam(r, "id")
		var req struct {
			Command string `json:"command"`
			WorkDir string `json:"work_dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Command == "" {
			writeError(w, http.StatusBadRequest, "command is required")
			return
		}

		sb, err := opts.Manager.GetSandbox(agentID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}

		res, err := sb.ExecCommand(r.Context(), req.Command, 30*time.Second, req.WorkDir)
		now := time.Now().UTC()
		logID := uuid.New().String()

		event := sandbox.ExecEvent{
			ID:         logID,
			AgentID:    agentID,
			Command:    req.Command,
			WorkDir:    req.WorkDir,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
			Timestamp:  now,
		}

		// Broadcast to live stream subscribers
		opts.Manager.GetBroadcaster().Publish(event)

		// Save to database
		_ = storage.RecordExecLog(opts.DB, &storage.ExecLog{
			ID:         logID,
			AgentID:    agentID,
			Command:    req.Command,
			WorkDir:    req.WorkDir,
			Stdout:     res.Stdout,
			Stderr:     res.Stderr,
			ExitCode:   res.ExitCode,
			DurationMs: res.DurationMs,
			Truncated:  res.Truncated,
			CreatedAt:  now,
		})

		writeJSON(w, http.StatusOK, event)
	})

	return r
}
