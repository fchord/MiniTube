package httpapi

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type progressReq struct {
	PositionMs int  `json:"positionMs"`
	DurationMs *int `json:"durationMs"`
	Completed  bool `json:"completed"`
}

type commentReq struct {
	Body     string  `json:"body"`
	ParentID *string `json:"parentId"`
}

type favInput struct {
	TargetType string `json:"targetType"`
	TargetID   string `json:"targetId"`
}

func (s *Server) putProgress(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canSeeVideo(v, &u.ID) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req progressReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.PositionMs < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid position")
		return
	}
	if err := s.store.UpsertProgress(r.Context(), u.ID, v.ID, req.PositionMs, req.DurationMs, req.Completed); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) likeVideo(w http.ResponseWriter, r *http.Request) {
	s.setVideoLike(w, r, true)
}

func (s *Server) unlikeVideo(w http.ResponseWriter, r *http.Request) {
	s.setVideoLike(w, r, false)
}

func (s *Server) setVideoLike(w http.ResponseWriter, r *http.Request, on bool) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canSeeVideo(v, &u.ID) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if on {
		err = s.store.Like(r.Context(), u.ID, "video", v.ID)
	} else {
		err = s.store.Unlike(r.Context(), u.ID, "video", v.ID)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listVideoComments(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canSeeVideo(v, viewerID(r)) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	items, err := s.store.ListComments(r.Context(), "video", v.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commentsJSON(items)})
}

func (s *Server) createVideoComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !canSeeVideo(v, &u.ID) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req commentReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	body := strings.TrimSpace(req.Body)
	if n := utf8.RuneCountInString(body); n < 1 || n > 2000 {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	var parent *uuid.UUID
	if req.ParentID != nil && *req.ParentID != "" {
		id, err := uuid.Parse(*req.ParentID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid parentId")
			return
		}
		parent = &id
	}
	c, err := s.store.CreateComment(r.Context(), u.ID, "video", v.ID, parent, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentJSON(c))
}

func (s *Server) listWatchHistory(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	items, err := s.store.ListWatchHistory(r.Context(), u.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		out = append(out, map[string]any{
			"video":         s.videoJSONOf(it.Video, false),
			"positionMs":    it.PositionMs,
			"durationMs":    it.DurationMs,
			"completed":     it.Completed,
			"lastWatchedAt": it.LastWatchedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) listMyCommentsReal(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	items, err := s.store.ListMyComments(r.Context(), u.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commentsJSON(items)})
}

func (s *Server) listFavoriteItems(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	listID, err := uuid.Parse(chi.URLParam(r, "listId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	ok, err := s.store.FavoriteListOwned(r.Context(), listID, u.ID)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	items, err := s.store.ListFavoriteItems(r.Context(), listID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		row := map[string]any{
			"targetType": it.TargetType,
			"targetId":   it.TargetID,
			"createdAt":  it.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
		if it.Video != nil {
			row["video"] = s.videoJSONOf(*it.Video, false)
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) addFavoriteItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	listID, err := uuid.Parse(chi.URLParam(r, "listId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	ok, err := s.store.FavoriteListOwned(r.Context(), listID, u.ID)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req favInput
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.TargetType != "video" && req.TargetType != "post" && req.TargetType != "live" {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid targetType")
		return
	}
	tid, err := s.parseFavoriteTarget(r.Context(), req.TargetType, req.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid targetId")
		return
	}
	if err := s.store.AddFavorite(r.Context(), listID, req.TargetType, tid); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFavoriteItem(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	listID, err := uuid.Parse(chi.URLParam(r, "listId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	ok, err := s.store.FavoriteListOwned(r.Context(), listID, u.ID)
	if err != nil || !ok {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	tid, err := s.parseFavoriteTarget(r.Context(), chi.URLParam(r, "targetType"), chi.URLParam(r, "targetId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := s.store.RemoveFavorite(r.Context(), listID, chi.URLParam(r, "targetType"), tid); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) parseFavoriteTarget(ctx context.Context, targetType, raw string) (string, error) {
	if targetType == "video" {
		return s.store.ResolveVideoID(ctx, raw)
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func commentJSON(c store.Comment) map[string]any {
	var parent any
	if c.ParentID != nil {
		parent = c.ParentID.String()
	}
	return map[string]any{
		"id":         c.ID.String(),
		"user":       publicUser(c.User),
		"targetType": c.TargetType,
		"targetId":   c.TargetID,
		"parentId":   parent,
		"body":       c.Body,
		"likeCount":  c.LikeCount,
		"createdAt":  c.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func commentsJSON(items []store.Comment) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		out = append(out, commentJSON(c))
	}
	return out
}
