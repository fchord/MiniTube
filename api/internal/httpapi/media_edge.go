package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type mediaEdge struct {
	ID      string   `json:"id"`
	Base    string   `json:"base"`
	Regions []string `json:"regions,omitempty"`
}

func (s *Server) mediaEdgeState(ctx context.Context) (enabled bool, edges []mediaEdge) {
	enabled = false
	edges = []mediaEdge{}
	if s == nil || s.store == nil {
		return enabled, edges
	}
	if s.cfg.IsTest() {
		return false, edges
	}
	if v, err := s.store.GetSiteSetting(ctx, "media_edge_enabled"); err == nil {
		enabled = strings.EqualFold(strings.TrimSpace(v), "on")
	}
	if v, err := s.store.GetSiteSetting(ctx, "media_edges"); err == nil && strings.TrimSpace(v) != "" {
		parsed, err := parseMediaEdgesJSON([]byte(v))
		if err == nil {
			edges = parsed
		}
	}
	return enabled, edges
}

func (s *Server) edgeKeepHosts(ctx context.Context) []string {
	_, edges := s.mediaEdgeState(ctx)
	out := make([]string, 0, len(edges))
	for _, e := range edges {
		if h := edgeHostname(e.Base); h != "" {
			out = append(out, h)
		}
	}
	return out
}

func (s *Server) rewriteMediaField(r *http.Request, m map[string]any, key string) {
	rewriteMediaField(r, m, key, s.edgeKeepHosts(r.Context()))
}

func (s *Server) rewriteMediaURLList(r *http.Request, items []map[string]any, key string) {
	rewriteMediaURLList(r, items, key, s.edgeKeepHosts(r.Context()))
}

func (s *Server) sameOriginMediaURL(r *http.Request, abs string) string {
	return sameOriginMediaURL(r, abs, s.edgeKeepHosts(r.Context()))
}

func parseMediaEdgesJSON(raw []byte) ([]mediaEdge, error) {
	var edges []mediaEdge
	if err := json.Unmarshal(raw, &edges); err != nil {
		return nil, apiError{status: http.StatusBadRequest, code: "bad_request", message: "mediaEdges must be an array"}
	}
	out := make([]mediaEdge, 0, len(edges))
	seen := map[string]bool{}
	for _, e := range edges {
		id := strings.TrimSpace(e.ID)
		base := strings.TrimSpace(e.Base)
		if id == "" || base == "" {
			return nil, apiError{status: http.StatusBadRequest, code: "bad_request", message: "mediaEdges id and base required"}
		}
		if seen[id] {
			return nil, apiError{status: http.StatusBadRequest, code: "bad_request", message: "mediaEdges id must be unique"}
		}
		u, err := url.Parse(base)
		if err != nil || u.Host == "" || strings.ToLower(u.Scheme) != "https" {
			return nil, apiError{status: http.StatusBadRequest, code: "bad_request", message: "mediaEdges base must be https URL"}
		}
		regions := make([]string, 0, len(e.Regions))
		for _, r := range e.Regions {
			r = strings.ToUpper(strings.TrimSpace(r))
			if r != "" {
				regions = append(regions, r)
			}
		}
		seen[id] = true
		out = append(out, mediaEdge{ID: id, Base: strings.TrimRight(base, "/"), Regions: regions})
	}
	return out, nil
}

func (s *Server) ensureCTCEdge(ctx context.Context) error {
	_, edges := s.mediaEdgeState(ctx)
	for _, e := range edges {
		if strings.EqualFold(e.ID, "ctc") {
			return nil
		}
	}
	host := strings.TrimSpace(s.cfg.MediaEdgeTLSHost)
	if host == "" {
		host = "ctc.minitube.19121122.xyz"
	}
	edges = append(edges, mediaEdge{
		ID:      "ctc",
		Base:    "https://" + host + ":18080",
		Regions: []string{"CN"},
	})
	raw, err := json.Marshal(edges)
	if err != nil {
		return err
	}
	return s.store.SetSiteSetting(ctx, "media_edges", string(raw))
}

func edgeHostname(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return hostnameOnly(u.Host)
}

func (s *Server) refreshEdgeHosts(ctx context.Context) {
	_, edges := s.mediaEdgeState(ctx)
	out := make([]string, 0, len(edges)+1)
	seen := map[string]bool{}
	if h := hostnameOnly(s.cfg.MediaEdgeTLSHost); h != "" {
		out = append(out, h)
		seen[h] = true
	}
	for _, e := range edges {
		if h := edgeHostname(e.Base); h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	s.edgeHostsMu.Lock()
	s.edgeHosts = out
	s.edgeHostsMu.Unlock()
}

func (s *Server) cachedEdgeHosts() []string {
	s.edgeHostsMu.Lock()
	defer s.edgeHostsMu.Unlock()
	return append([]string(nil), s.edgeHosts...)
}

func (s *Server) requestIsEdgeHost(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.URL != nil && r.URL.Path == "/healthz" {
		return false
	}
	if r.TLS != nil {
		return true
	}
	reqHost := hostnameOnly(r.Host)
	if reqHost == "" {
		return false
	}
	for _, h := range s.cachedEdgeHosts() {
		if reqHost == h {
			return true
		}
	}
	if h := hostnameOnly(s.cfg.MediaEdgeTLSHost); h != "" && reqHost == h {
		return true
	}
	return false
}

func edgePathAllowed(method, path string) bool {
	m := strings.ToUpper(method)
	if m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions {
		return false
	}
	if path == "/v1/media/edge-probe" {
		return true
	}
	if strings.HasPrefix(path, "/v1/media/") {
		return true
	}
	if strings.HasPrefix(path, "/v1/live/") && strings.Contains(path, "/hls/") {
		return true
	}
	return false
}

func (s *Server) edgeHostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requestIsEdgeHost(r) {
			next.ServeHTTP(w, r)
			return
		}
		if edgePathAllowed(r.Method, r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func (s *Server) mediaEdgeProbe(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusNoContent)
}

func countryHint(r *http.Request) string {
	if r == nil {
		return ""
	}
	cc := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if cc == "" || cc == "XX" || cc == "T1" {
		return ""
	}
	return cc
}
