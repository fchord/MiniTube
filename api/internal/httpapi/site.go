package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

func (s *Server) getPublicSite(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.siteJSON(r))
}

func (s *Server) siteJSON(r *http.Request) map[string]any {
	engine := "legacy"
	if v, err := s.store.GetSiteSetting(r.Context(), "shorts_engine"); err == nil && v != "" {
		engine = v
	}
	if engine != "webcodecs" {
		engine = "legacy"
	}
	enabled, edges := s.mediaEdgeState(r.Context())
	out := map[string]any{
		"shortsEngine":     engine,
		"mediaEdgeEnabled": enabled,
		"mediaEdges":       edges,
		"mediaHint":        countryHint(r),
	}
	return out
}

func (s *Server) isAdminUser(id uuid.UUID) bool {
	want := id.String()
	for _, x := range s.cfg.AdminUserIDs {
		if strings.EqualFold(strings.TrimSpace(x), want) {
			return true
		}
	}
	return false
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := currentUser(r)
		if !ok || !s.isAdminUser(u.ID) {
			writeError(w, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) patchAdminSite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ShortsEngine     *string         `json:"shortsEngine"`
		MediaEdgeEnabled *bool           `json:"mediaEdgeEnabled"`
		MediaEdges       json.RawMessage `json:"mediaEdges"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if req.ShortsEngine != nil {
		v := strings.TrimSpace(*req.ShortsEngine)
		if v != "legacy" && v != "webcodecs" {
			writeError(w, http.StatusBadRequest, "bad_request", "shortsEngine must be legacy or webcodecs")
			return
		}
		if err := s.store.SetSiteSetting(r.Context(), "shorts_engine", v); err != nil {
			writeErr(w, err)
			return
		}
	}
	if req.MediaEdges != nil {
		edges, err := parseMediaEdgesJSON(req.MediaEdges)
		if err != nil {
			writeErr(w, err)
			return
		}
		raw, err := json.Marshal(edges)
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := s.store.SetSiteSetting(r.Context(), "media_edges", string(raw)); err != nil {
			writeErr(w, err)
			return
		}
	}
	if req.MediaEdgeEnabled != nil {
		if *req.MediaEdgeEnabled {
			if err := s.ensureCTCEdge(r.Context()); err != nil {
				writeErr(w, err)
				return
			}
		}
		v := "off"
		if *req.MediaEdgeEnabled {
			v = "on"
		}
		if err := s.store.SetSiteSetting(r.Context(), "media_edge_enabled", v); err != nil {
			writeErr(w, err)
			return
		}
	}
	if req.MediaEdges != nil || req.MediaEdgeEnabled != nil {
		s.refreshEdgeHosts(r.Context())
	}
	writeJSON(w, http.StatusOK, s.siteJSON(r))
}
