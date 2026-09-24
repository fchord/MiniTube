package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"minitube/api/internal/ident"
	"minitube/api/internal/store"
)

type createChannelReq struct {
	Name        string   `json:"name"`
	Handle      string   `json:"handle"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
}

type updateChannelReq struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Tags        *[]string `json:"tags"`
	Visibility  *string   `json:"visibility"`
	IsDefault   *bool     `json:"isDefault"`
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	var req createChannelReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := ident.ValidChannelName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	handle, err := ident.NormalizeHandle(req.Handle)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	ch, err := s.store.CreateChannel(r.Context(), u, req.Name, handle, req.Description, req.Tags)
	if err != nil {
		writeConflictOr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusCreated, channelJSONOf(ch, &sub))
}

func (s *Server) getChannel(w http.ResponseWriter, r *http.Request) {
	handle, err := ident.NormalizeHandle(chi.URLParam(r, "handle"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	ch, err := s.store.GetChannelByHandle(r.Context(), handle)
	if err != nil {
		writeErr(w, err)
		return
	}
	vid := viewerID(r)
	if !visibleTo(ch, vid) {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var sub *bool
	if vid != nil {
		ok, err := s.store.IsSubscribed(r.Context(), *vid, ch.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		sub = &ok
	}
	j, err := s.channelPageJSON(r, ch, sub)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) channelPageJSON(r *http.Request, ch store.Channel, sub *bool) (channelJSON, error) {
	j := channelJSONOf(ch, sub)
	if j.AvatarURL == nil || *j.AvatarURL == "" {
		if owner, err := s.store.GetUserByID(r.Context(), ch.OwnerUserID); err == nil {
			j.AvatarURL = owner.AvatarURL
		}
	}
	include := false
	if vid := viewerID(r); vid != nil && *vid == ch.OwnerUserID {
		include = true
	}
	counts, err := s.store.CountChannelContent(r.Context(), ch.ID, include)
	if err != nil {
		return j, err
	}
	j.VideoCount = &counts.Videos
	j.ShortCount = &counts.Shorts
	j.LiveCount = &counts.Lives
	j.PostCount = &counts.Posts
	return j, nil
}

func (s *Server) patchChannel(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	handle, err := ident.NormalizeHandle(chi.URLParam(r, "handle"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	var req updateChannelReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Name != nil {
		if err := ident.ValidChannelName(*req.Name); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	if req.Visibility != nil {
		switch *req.Visibility {
		case "public", "unlisted", "private":
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "invalid visibility")
			return
		}
	}
	ch, err := s.store.UpdateChannel(r.Context(), handle, u.ID, store.ChannelUpdate{
		Name:        req.Name,
		Description: req.Description,
		Tags:        req.Tags,
		Visibility:  req.Visibility,
		IsDefault:   req.IsDefault,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	sub := false
	writeJSON(w, http.StatusOK, channelJSONOf(ch, &sub))
}

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	ch, err := s.loadVisibleChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.Subscribe(r.Context(), u.ID, ch.ID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	handle, err := ident.NormalizeHandle(chi.URLParam(r, "handle"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	ch, err := s.store.GetChannelByHandle(r.Context(), handle)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.Unsubscribe(r.Context(), u.ID, ch.ID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) loadVisibleChannel(r *http.Request) (store.Channel, error) {
	handle, err := ident.NormalizeHandle(chi.URLParam(r, "handle"))
	if err != nil {
		return store.Channel{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	ch, err := s.store.GetChannelByHandle(r.Context(), handle)
	if err != nil {
		return store.Channel{}, err
	}
	if !visibleTo(ch, viewerID(r)) {
		return store.Channel{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return ch, nil
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	u, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil || u.Status != "active" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	writeJSON(w, http.StatusOK, publicUser(u))
}

func (s *Server) listUserChannels(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")
	u, err := s.store.GetUserByUsername(r.Context(), username)
	if err != nil || u.Status != "active" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	chs, err := s.store.ListChannelsByOwner(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	vid := viewerID(r)
	out := make([]channelJSON, 0, len(chs))
	for _, ch := range chs {
		if !visibleTo(ch, vid) {
			continue
		}
		var sub *bool
		if vid != nil {
			ok, err := s.store.IsSubscribed(r.Context(), *vid, ch.ID)
			if err != nil {
				writeErr(w, err)
				return
			}
			sub = &ok
		}
		out = append(out, channelJSONOf(ch, sub))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
