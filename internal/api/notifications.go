// notifications.go — REST endpoints for managing notification agents.
// Provides full CRUD for the Config.NotificationAgents slice plus
// inline + saved-agent test handlers. Mirrors the clonarr v2.3 shape
// per docs/notification-agents-pattern.md.
//
// Routes (registered in routes.go):
//
//	GET    /api/notifications/agents           → handleListNotificationAgents
//	POST   /api/notifications/agents           → handleCreateNotificationAgent
//	PUT    /api/notifications/agents/{id}      → handleUpdateNotificationAgent
//	DELETE /api/notifications/agents/{id}      → handleDeleteNotificationAgent
//	POST   /api/notifications/agents/test      → handleTestNotificationAgentInline
//	POST   /api/notifications/agents/{id}/test → handleTestNotificationAgent

package api

import (
	"encoding/json"
	"net/http"

	"resolvarr/internal/core"
)

// handleListNotificationAgents returns all configured notification agents
// with credentials masked so webhook URLs and tokens are never exposed
// to the frontend.
func (s *Server) handleListNotificationAgents(w http.ResponseWriter, r *http.Request) {
	cfg := s.App.Config.Get()
	agents := cfg.NotificationAgents
	if agents == nil {
		agents = []core.NotificationAgent{}
	}
	for i, a := range agents {
		agents[i].Config = core.MaskNotificationAgentConfig(a.Type, a.Config)
	}
	writeJSON(w, agents)
}

// handleCreateNotificationAgent validates and persists a new notification
// agent. The agent receives a server-generated ID. Multiple agents of
// the same provider type are allowed (e.g. two Discord channels).
func (s *Server) handleCreateNotificationAgent(w http.ResponseWriter, r *http.Request) {
	var agent core.NotificationAgent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&agent); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	if err := core.ValidateNotificationAgent(agent); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	created, err := s.App.Config.AddNotificationAgent(agent)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	created.Config = core.MaskNotificationAgentConfig(created.Type, created.Config)
	writeJSON(w, created)
}

// handleUpdateNotificationAgent replaces an existing agent by ID.
// Credential fields that arrive as masked placeholders are transparently
// restored to their stored values via PreserveNotificationAgentConfig.
func (s *Server) handleUpdateNotificationAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var agent core.NotificationAgent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&agent); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	existing, found := s.App.Config.GetNotificationAgent(id)
	if !found {
		writeError(w, 404, "notification agent not found")
		return
	}
	agent.Config = core.PreserveNotificationAgentConfig(agent.Type, agent.Config, existing.Config)
	if err := core.ValidateNotificationAgent(agent); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	updated, err := s.App.Config.UpdateNotificationAgent(id, agent)
	if err != nil {
		writeError(w, 404, err.Error())
		return
	}
	updated.Config = core.MaskNotificationAgentConfig(updated.Type, updated.Config)
	writeJSON(w, updated)
}

// handleDeleteNotificationAgent removes an agent by ID.
func (s *Server) handleDeleteNotificationAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.App.Config.DeleteNotificationAgent(id); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "deleted"})
}

// handleTestNotificationAgentInline tests credentials sent inline in the
// request body without requiring a saved agent ID. Used by the add-agent
// modal so users can verify connectivity before committing the config.
func (s *Server) handleTestNotificationAgentInline(w http.ResponseWriter, r *http.Request) {
	var req core.NotificationAgent
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, 400, "invalid body: "+err.Error())
		return
	}
	// For an inline test, the user has just typed credentials into the
	// modal form. If the form sent the masked placeholder back (because
	// they only edited Name/Events), pull the stored creds in by ID
	// when the request includes one. Without an ID, OR with an ID that
	// doesn't match a saved agent, the test fires with whatever was
	// sent — Validate then surfaces structural errors before the
	// per-provider Test() reaches a real network endpoint.
	if req.ID != "" {
		if existing, ok := s.App.Config.GetNotificationAgent(req.ID); ok {
			req.Config = core.PreserveNotificationAgentConfig(req.Type, req.Config, existing.Config)
		}
	}
	if err := core.ValidateNotificationAgent(req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.runNotificationAgentTest(w, r, req)
}

// handleTestNotificationAgent fires test messages for an already-saved
// agent. Looks up the agent by path parameter {id} and delegates.
func (s *Server) handleTestNotificationAgent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, ok := s.App.Config.GetNotificationAgent(id)
	if !ok {
		writeError(w, 404, "notification agent not found")
		return
	}
	s.runNotificationAgentTest(w, r, existing)
}

// runNotificationAgentTest executes the test logic for any notification
// agent and writes the JSON response. Shared by both the inline and
// saved-agent test handlers.
func (s *Server) runNotificationAgentTest(w http.ResponseWriter, r *http.Request, req core.NotificationAgent) {
	results, err := core.TestNotificationAgent(r.Context(), s.App, req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"results": results})
}
