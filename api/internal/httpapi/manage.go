package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type manageReq struct {
	Action   string   `json:"action"`
	VideoIDs []string `json:"videoIds"`
	LiveIDs  []string `json:"liveIds"`
}

type updateLiveReq struct {
	Visibility *string `json:"visibility"`
}

func (s *Server) loadOwnedChannel(r *http.Request) (store.Channel, error) {
	u, ok := currentUser(r)
	if !ok {
		return store.Channel{}, apiError{status: http.StatusUnauthorized, code: "unauthenticated", message: "unauthenticated"}
	}
	ch, err := s.loadVisibleChannel(r)
	if err != nil {
		return store.Channel{}, err
	}
	if ch.OwnerUserID != u.ID {
		return store.Channel{}, apiError{status: http.StatusForbidden, code: "forbidden", message: "forbidden"}
	}
	return ch, nil
}

func liveBusyErr(l store.Live) error {
	if l.Status == "live" {
		return apiError{status: http.StatusConflict, code: "conflict", message: "直播进行中，无法操作"}
	}
	return nil
}

func (s *Server) listChannelTrash(w http.ResponseWriter, r *http.Request) {
	ch, err := s.loadOwnedChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	limit := parseLimit(r)
	videos, err := s.store.ListDeletedVideos(r.Context(), ch.ID, "long", limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	shorts, err := s.store.ListDeletedVideos(r.Context(), ch.ID, "short", limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	lives, err := s.store.ListDeletedLives(r.Context(), ch.ID, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	vJSON := make([]map[string]any, 0, len(videos))
	for _, v := range videos {
		v.Channel = ch
		vJSON = append(vJSON, s.videoJSONOf(v, false))
	}
	sJSON := make([]map[string]any, 0, len(shorts))
	for _, v := range shorts {
		v.Channel = ch
		sJSON = append(sJSON, s.videoJSONOf(v, false))
	}
	lJSON := make([]map[string]any, 0, len(lives))
	for _, l := range lives {
		l.Channel = ch
		lJSON = append(lJSON, s.liveJSONOf(r, l, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"videos": vJSON,
		"shorts": sJSON,
		"lives":  lJSON,
	})
}

func (s *Server) manageChannel(w http.ResponseWriter, r *http.Request) {
	ch, err := s.loadOwnedChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var req manageReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	switch req.Action {
	case "hide", "publish", "recycle", "restore", "purge":
	default:
		writeError(w, http.StatusBadRequest, "bad_request", "invalid action")
		return
	}
	if len(req.VideoIDs) == 0 && len(req.LiveIDs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "nothing selected")
		return
	}
	ctx := r.Context()
	for _, raw := range req.VideoIDs {
		id, err := s.store.ResolveVideoID(ctx, raw)
		if err != nil {
			continue
		}
		v, err := s.store.GetVideo(ctx, id)
		if err != nil || v.ChannelID != ch.ID {
			continue
		}
		if err := s.applyVideoAction(ctx, v, req.Action); err != nil {
			writeErr(w, err)
			return
		}
	}
	for _, raw := range req.LiveIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		l, err := s.store.GetLive(ctx, id)
		if err != nil || l.ChannelID != ch.ID {
			continue
		}
		if err := s.applyLiveAction(ctx, l, req.Action); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) applyVideoAction(ctx context.Context, v store.Video, action string) error {
	switch action {
	case "hide":
		if v.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetVideoVisibility(ctx, v.ID, "private")
		return err
	case "publish":
		if v.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetVideoVisibility(ctx, v.ID, "public")
		return err
	case "recycle":
		if v.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetVideoVisibility(ctx, v.ID, "deleted")
		return err
	case "restore":
		if v.Visibility != "deleted" {
			return nil
		}
		_, err := s.store.SetVideoVisibility(ctx, v.ID, "public")
		return err
	case "purge":
		return s.purgeVideo(ctx, v)
	}
	return nil
}

func (s *Server) applyLiveAction(ctx context.Context, l store.Live, action string) error {
	if err := liveBusyErr(l); err != nil {
		return nil
	}
	switch action {
	case "hide":
		if l.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetLiveVisibility(ctx, l.ID, "private")
		return err
	case "publish":
		if l.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetLiveVisibility(ctx, l.ID, "public")
		return err
	case "recycle":
		if l.Visibility == "deleted" {
			return nil
		}
		_, err := s.store.SetLiveVisibility(ctx, l.ID, "deleted")
		return err
	case "restore":
		if l.Visibility != "deleted" {
			return nil
		}
		_, err := s.store.SetLiveVisibility(ctx, l.ID, "public")
		return err
	case "purge":
		return s.purgeLive(ctx, l)
	}
	return nil
}

func (s *Server) patchLive(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	live, err := s.loadLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if live.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if live.Visibility == "deleted" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := liveBusyErr(live); err != nil {
		writeErr(w, err)
		return
	}
	var req updateLiveReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Visibility == nil {
		writeJSON(w, http.StatusOK, s.liveJSONOf(r, live, nil))
		return
	}
	if *req.Visibility != "public" && *req.Visibility != "private" {
		writeError(w, http.StatusBadRequest, "bad_request", "visibility must be public or private")
		return
	}
	updated, err := s.store.SetLiveVisibility(r.Context(), live.ID, *req.Visibility)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.liveJSONOf(r, updated, nil))
}

func (s *Server) deleteLive(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	live, err := s.loadLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if live.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if r.URL.Query().Get("purge") == "1" {
		if err := liveBusyErr(live); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.purgeLive(r.Context(), live); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if live.Visibility == "deleted" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := liveBusyErr(live); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.store.SetLiveVisibility(r.Context(), live.ID, "deleted"); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) purgeVideo(ctx context.Context, v store.Video) error {
	if err := s.store.DeleteVideo(ctx, v.ID); err != nil {
		return err
	}
	s.uploads.RemovePrefix("sources/" + v.ID)
	s.uploads.RemovePrefix("hls/" + v.ID)
	s.uploads.AbortVideoTmp(v.ID)
	return nil
}

func (s *Server) purgeLive(ctx context.Context, live store.Live) error {
	if err := s.store.DeleteLive(ctx, live.ID); err != nil {
		return err
	}
	if live.IngestKey != "" {
		if s.live != nil {
			_ = os.RemoveAll(s.live.ArchiveDir(live.IngestKey))
		}
		if s.cfg.SRSHLSDir != "" {
			matches, _ := filepath.Glob(filepath.Join(s.cfg.SRSHLSDir, "live", live.IngestKey+"*"))
			for _, p := range matches {
				_ = os.RemoveAll(p)
			}
		}
	}
	return nil
}
