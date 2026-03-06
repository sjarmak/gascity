package api

import (
	"net/http"
)

type packResponse struct {
	Name   string `json:"name"`
	Source string `json:"source,omitempty"`
	Ref    string `json:"ref,omitempty"`
	Path   string `json:"path,omitempty"`
}

func (s *Server) handlePackList(w http.ResponseWriter, _ *http.Request) {
	cfg := s.state.Config()
	packs := make([]packResponse, 0, len(cfg.Packs))
	for name, src := range cfg.Packs {
		packs = append(packs, packResponse{
			Name:   name,
			Source: src.Source,
			Ref:    src.Ref,
			Path:   src.Path,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"packs": packs})
}
