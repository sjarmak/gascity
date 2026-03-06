package api

import (
	"net/http"

	"github.com/gastownhall/gascity/internal/automations"
)

type automationResponse struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	Gate        string `json:"gate"`
	Interval    string `json:"interval,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	Check       string `json:"check,omitempty"`
	On          string `json:"on,omitempty"`
	Formula     string `json:"formula,omitempty"`
	Exec        string `json:"exec,omitempty"`
	Pool        string `json:"pool,omitempty"`
	Timeout     string `json:"timeout,omitempty"`
	Enabled     bool   `json:"enabled"`
	Rig         string `json:"rig,omitempty"`
}

func (s *Server) handleAutomationList(w http.ResponseWriter, _ *http.Request) {
	aa := s.state.Automations()
	resp := make([]automationResponse, len(aa))
	for i, a := range aa {
		resp[i] = toAutomationResponse(a)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"automations": resp,
	})
}

func (s *Server) handleAutomationGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	aa := s.state.Automations()
	for _, a := range aa {
		if a.Name == name || a.ScopedName() == name {
			writeJSON(w, http.StatusOK, toAutomationResponse(a))
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "automation "+name+" not found")
}

func toAutomationResponse(a automations.Automation) automationResponse {
	typ := "formula"
	if a.IsExec() {
		typ = "exec"
	}
	return automationResponse{
		Name:        a.Name,
		Description: a.Description,
		Type:        typ,
		Gate:        a.Gate,
		Interval:    a.Interval,
		Schedule:    a.Schedule,
		Check:       a.Check,
		On:          a.On,
		Formula:     a.Formula,
		Exec:        a.Exec,
		Pool:        a.Pool,
		Timeout:     a.Timeout,
		Enabled:     a.IsEnabled(),
		Rig:         a.Rig,
	}
}
