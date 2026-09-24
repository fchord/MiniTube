package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"minitube/api/internal/liveorigin"
	"minitube/api/internal/store"
)

type srsHookReq struct {
	Action string `json:"action"`
	App    string `json:"app"`
	Stream string `json:"stream"`
	File   string `json:"file"`
	M3U8   string `json:"m3u8"`
}

func (s *Server) srsAuthorized(r *http.Request) bool {
	want := s.cfg.SRSHookSecret
	got := r.URL.Query().Get("secret")
	if want == "" || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (s *Server) srsOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"code":0}`))
}

func (s *Server) srsDeny(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": msg})
}

func (s *Server) readSRSHook(w http.ResponseWriter, r *http.Request) (srsHookReq, bool) {
	if !s.srsAuthorized(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "msg": "unauthorized"})
		return srsHookReq{}, false
	}
	var req srsHookReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.srsDeny(w, "invalid json")
		return srsHookReq{}, false
	}
	return req, true
}

func (s *Server) srsOnPublish(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readSRSHook(w, r)
	if !ok {
		return
	}
	if s.live == nil || req.Stream == "" {
		s.srsDeny(w, "missing stream")
		return
	}
	if err := s.live.OnPublish(r.Context(), req.Stream); err != nil {
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, liveorigin.ErrLiveEnded) {
			s.srsDeny(w, err.Error())
			return
		}
		slog.Warn("srs on_publish", "stream", req.Stream, "err", err)
		s.srsDeny(w, "internal")
		return
	}
	s.srsOK(w)
}

func (s *Server) srsOnUnpublish(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readSRSHook(w, r)
	if !ok {
		return
	}
	if s.live != nil && req.Stream != "" {
		if err := s.live.OnUnpublish(r.Context(), req.Stream); err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Warn("srs on_unpublish", "stream", req.Stream, "err", err)
		}
	}
	s.srsOK(w)
}

func (s *Server) srsOnHLS(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readSRSHook(w, r)
	if !ok {
		return
	}
	if s.live != nil && req.Stream != "" {
		if err := s.live.OnHLS(r.Context(), req.Stream, req.File, req.M3U8); err != nil {
			slog.Warn("srs on_hls", "stream", req.Stream, "err", err)
		}
	}
	s.srsOK(w)
}
