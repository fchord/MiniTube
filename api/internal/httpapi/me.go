package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"minitube/api/internal/auth"
	"minitube/api/internal/ident"
	"minitube/api/internal/store"
)

type updateMeReq struct {
	Nickname *string `json:"nickname"`
	Bio      *string `json:"bio"`
	Username *string `json:"username"`
}

type avatarReq struct {
	MimeType string `json:"mimeType"`
	Complete bool   `json:"complete"`
}

type changePasswordReq struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	ch, err := s.store.DefaultChannel(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusOK, privateUser(u, ch, &sub))
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	var req updateMeReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	var nickname, bio, username *string
	if req.Nickname != nil {
		n, err := ident.NormalizeNickname(*req.Nickname, u.Username)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		nickname = &n
	}
	if req.Bio != nil {
		if err := ident.ValidBio(*req.Bio); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		bio = req.Bio
	}
	if req.Username != nil {
		n, err := ident.NormalizeUsername(*req.Username)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		username = &n
	}
	updated, err := s.store.UpdateMe(r.Context(), u.ID, nickname, bio, username)
	if err != nil {
		writeConflictOr(w, err)
		return
	}
	ch, err := s.store.DefaultChannel(r.Context(), updated.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusOK, privateUser(updated, ch, &sub))
}

func (s *Server) avatar(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	var req avatarReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Complete {
		url, err := s.uploads.CompleteAvatar(u.ID.String())
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if err := s.store.SetAvatarURL(r.Context(), u.ID, url); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"uploadUrl": url,
			"objectKey": url,
			"headers":   map[string]string{},
		})
		return
	}
	if req.MimeType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "mimeType required")
		return
	}
	uploadURL, objectKey, headers, err := s.uploads.BeginAvatar(u.ID.String(), req.MimeType)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uploadUrl": uploadURL,
		"objectKey": objectKey,
		"headers":   headers,
	})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	var req changePasswordReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if !auth.CheckPassword(u.PasswordHash, req.CurrentPassword) {
		writeError(w, http.StatusBadRequest, "bad_request", "原密码不正确")
		return
	}
	if err := ident.ValidPassword(req.NewPassword); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetPasswordHash(r.Context(), u.ID, hash); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mySubscriptions(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	limit := parseLimit(r)
	ct, cidRaw, ok := decodeCursor(r.URL.Query().Get("cursor"))
	var items []store.SubscribedChannel
	var err error
	if ok {
		cid, perr := uuid.Parse(cidRaw)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid cursor")
			return
		}
		items, err = s.store.ListSubscriptions(r.Context(), u.ID, &ct, &cid, limit+1)
	} else {
		items, err = s.store.ListSubscriptions(r.Context(), u.ID, nil, nil, limit+1)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		last := items[limit-1]
		c := encodeCursor(last.SubscribedAt, last.ID.String())
		next = &c
		items = items[:limit]
	}
	out := make([]channelJSON, 0, len(items))
	tr := true
	for _, ch := range items {
		j := channelJSONOf(ch.Channel, &tr)
		out = append(out, j)
	}
	body := map[string]any{"items": out}
	if next != nil {
		body["nextCursor"] = *next
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) myFavorites(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	lists, err := s.store.ListFavoriteLists(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	items := make([]map[string]string, 0, len(lists))
	for _, l := range lists {
		items = append(items, map[string]string{
			"id":   l.ID.String(),
			"kind": l.Kind,
			"name": l.Name,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func parseLimit(r *http.Request) int {
	n := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			n = parsed
		}
	}
	if n < 1 {
		n = 1
	}
	if n > 50 {
		n = 50
	}
	return n
}
