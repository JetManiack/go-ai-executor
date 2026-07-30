package restapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/JetManiack/go-ai-executor/internal/humanauth"
	"github.com/JetManiack/go-ai-executor/internal/sandbox"
	"github.com/JetManiack/go-ai-executor/internal/storage"
)

// sandboxResponse is one row of the sandbox list.
//
// Live is false for an agent that has not made a tool call since this process
// started: sandboxes are instantiated lazily, so such an agent has no directory,
// no processes and no retained output — but it can still be blocked, which is
// what stops its next call.
type sandboxResponse struct {
	ActorID         string                `json:"actor_id"`
	DisplayName     string                `json:"display_name"`
	Live            bool                  `json:"live"`
	RunningCommands int                   `json:"running_commands"`
	Watchers        int                   `json:"watchers"`
	LastSeq         uint64                `json:"last_seq"`
	Block           *storage.SandboxBlock `json:"block,omitempty"`
}

type blockRequest struct {
	Reason string `json:"reason"`
}

type blockResponse struct {
	Block           *storage.SandboxBlock `json:"block"`
	KilledProcesses int                   `json:"killed_processes"`
}

func listSandboxesHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agents, err := storage.ListAgents(opts.DB)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		blocks, err := storage.ActiveSandboxBlocks(opts.DB)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		live := opts.Manager.LiveSandboxes()

		resp := make([]sandboxResponse, 0, len(agents))
		for _, agent := range agents {
			row := sandboxResponse{
				ActorID:     agent.ID,
				DisplayName: agent.DisplayName,
				Block:       blocks[agent.ID],
				LastSeq:     opts.Manager.Broadcaster().LastSeq(agent.ID),
				Watchers:    opts.Manager.Broadcaster().WatcherCount(agent.ID),
			}
			if sb, ok := live[agent.ID]; ok {
				row.Live = true
				row.RunningCommands = sb.RunningCommands()
			}
			resp = append(resp, row)
		}

		// Blocked first, then busiest: an operator opening this list is looking
		// for something to act on, not an alphabetical directory.
		sort.SliceStable(resp, func(i, j int) bool {
			if (resp[i].Block != nil) != (resp[j].Block != nil) {
				return resp[i].Block != nil
			}
			if resp[i].RunningCommands != resp[j].RunningCommands {
				return resp[i].RunningCommands > resp[j].RunningCommands
			}
			return resp[i].DisplayName < resp[j].DisplayName
		})

		writeJSON(w, http.StatusOK, resp)
	}
}

func getSandboxHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actorID := chi.URLParam(r, "id")

		agent, err := storage.GetActorByID(opts.DB, actorID)
		if errors.Is(err, storage.ErrActorNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		block, err := storage.ActiveSandboxBlock(opts.DB, actorID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		resp := sandboxResponse{
			ActorID:     agent.ID,
			DisplayName: agent.DisplayName,
			Block:       block,
			LastSeq:     opts.Manager.Broadcaster().LastSeq(actorID),
			Watchers:    opts.Manager.Broadcaster().WatcherCount(actorID),
		}
		if sb, ok := opts.Manager.LiveSandboxes()[actorID]; ok {
			resp.Live = true
			resp.RunningCommands = sb.RunningCommands()
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// blockSandboxHandler is the emergency stop: it records the block, then kills
// every process group running in the sandbox.
//
// The block is recorded first, deliberately. Killing first would leave a window
// in which the processes are gone but nothing stops the agent from starting more
// — which, for the runaway loop this button exists to stop, means it simply
// restarts. Recording first makes the refusal effective before the teardown.
func blockSandboxHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actorID := chi.URLParam(r, "id")

		admin, ok := humanauth.ActorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("no authenticated actor"))
			return
		}

		var req blockRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("body must be a JSON object with reason"))
			return
		}
		req.Reason = strings.TrimSpace(req.Reason)

		if _, err := storage.GetActorByID(opts.DB, actorID); errors.Is(err, storage.ErrActorNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		} else if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		block, err := storage.BlockSandbox(opts.DB, actorID, admin, req.Reason)
		switch {
		case errors.Is(err, storage.ErrEmptyReason):
			writeError(w, http.StatusBadRequest, err)
			return
		case errors.Is(err, storage.ErrAlreadyBlocked):
			writeError(w, http.StatusConflict, err)
			return
		case err != nil:
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		killed, killErr := opts.Manager.KillSandbox(actorID, admin.DisplayName, req.Reason)
		if killErr != nil {
			// The block is already in force, which is the part that matters, so
			// this is reported rather than rolled back: a partially-killed
			// sandbox that cannot start anything new is a safe state.
			slog.Error("kill sandbox processes", "actor_id", actorID, "error", killErr)
		}
		if killed > 0 {
			if err := storage.RecordKilledProcesses(opts.DB, block.ID, killed); err != nil {
				slog.Error("record killed process count", "block_id", block.ID, "error", err)
			}
			block.KilledProcesses = killed
		}

		opts.Manager.Broadcaster().Publish(blockedEvent(actorID, admin.DisplayName, req.Reason))
		writeJSON(w, http.StatusCreated, blockResponse{Block: block, KilledProcesses: killed})
	}
}

func releaseSandboxHandler(opts Options) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actorID := chi.URLParam(r, "id")

		admin, ok := humanauth.ActorFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, errors.New("no authenticated actor"))
			return
		}

		err := storage.ReleaseSandbox(opts.DB, actorID, admin)
		if errors.Is(err, storage.ErrNotBlocked) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		opts.Manager.Broadcaster().Publish(releasedEvent(actorID, admin.DisplayName))
		w.WriteHeader(http.StatusNoContent)
	}
}

// blockedEvent and releasedEvent put administrative state changes into the
// terminal stream itself, so a human watching sees why output stopped — or that
// it is expected to resume — instead of watching it simply go quiet.
func blockedEvent(actorID, byActor, reason string) sandbox.Event {
	return sandbox.Event{
		SandboxID: actorID,
		Kind:      sandbox.EventBlocked,
		At:        time.Now().UTC(),
		ByActor:   byActor,
		Reason:    reason,
	}
}

func releasedEvent(actorID, byActor string) sandbox.Event {
	return sandbox.Event{
		SandboxID: actorID,
		Kind:      sandbox.EventReleased,
		At:        time.Now().UTC(),
		ByActor:   byActor,
	}
}
