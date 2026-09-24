package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type createPostReq struct {
	Body             string   `json:"body"`
	ChannelID        *string  `json:"channelId"`
	MentionedVideoID *string  `json:"mentionedVideoId"`
	ImageObjectKeys  []string `json:"imageObjectKeys"`
}

type imageUploadReq struct {
	MimeType string `json:"mimeType"`
}

func postJSON(p store.Post, imageURLs []string, liked bool) map[string]any {
	var ch any
	if p.Channel != nil {
		ch = channelJSONOf(*p.Channel, nil)
	}
	var mentioned any
	if p.MentionedVideoID != nil {
		mentioned = *p.MentionedVideoID
	}
	if imageURLs == nil {
		imageURLs = []string{}
	}
	return map[string]any{
		"id":               p.ID.String(),
		"author":           publicUser(p.Author),
		"channel":          ch,
		"body":             p.Body,
		"imageUrls":        imageURLs,
		"mentionedVideoId": mentioned,
		"likeCount":        p.LikeCount,
		"commentCount":     p.CommentCount,
		"viewCount":        p.ViewCount,
		"liked":            liked,
		"createdAt":        p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Server) postJSONOf(r *http.Request, p store.Post) map[string]any {
	urls := make([]string, 0, len(p.ImageKeys))
	for _, k := range p.ImageKeys {
		urls = append(urls, s.uploads.URL(k))
	}
	liked := false
	if vid := viewerID(r); vid != nil {
		liked, _ = s.store.PostLiked(r.Context(), *vid, p.ID)
	}
	return postJSON(p, urls, liked)
}

func (s *Server) beginPostImage(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	var req imageUploadReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.MimeType == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "mimeType required")
		return
	}
	upURL, key, headers, err := s.uploads.BeginPostImage(u.ID.String(), req.MimeType)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploadUrl": upURL, "objectKey": key, "headers": headers})
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	var req createPostReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	body := strings.TrimSpace(req.Body)
	if n := utf8.RuneCountInString(body); n < 1 || n > 2000 {
		writeError(w, http.StatusBadRequest, "bad_request", "body must be 1-2000 characters")
		return
	}
	if len(req.ImageObjectKeys) > 9 {
		writeError(w, http.StatusBadRequest, "bad_request", "at most 9 images")
		return
	}
	for _, k := range req.ImageObjectKeys {
		if !s.uploads.KeyOwnedBy(u.ID.String(), k) {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid imageObjectKeys")
			return
		}
	}
	var channelID *uuid.UUID
	if req.ChannelID != nil && *req.ChannelID != "" {
		id, err := uuid.Parse(*req.ChannelID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid channelId")
			return
		}
		ch, err := s.store.GetChannelByID(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if ch.OwnerUserID != u.ID {
			writeError(w, http.StatusForbidden, "forbidden", "not your channel")
			return
		}
		channelID = &id
	}
	var mentioned *string
	if req.MentionedVideoID != nil && *req.MentionedVideoID != "" {
		id, err := s.store.ResolveVideoID(r.Context(), *req.MentionedVideoID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid mentionedVideoId")
			return
		}
		v, err := s.store.GetVideo(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !canSeeVideo(v, &u.ID) {
			writeError(w, http.StatusBadRequest, "bad_request", "video not available")
			return
		}
		mentioned = &id
	}
	post, err := s.store.CreatePost(r.Context(), u, channelID, mentioned, body, req.ImageObjectKeys)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.postJSONOf(r, post))
}

func (s *Server) getPost(w http.ResponseWriter, r *http.Request) {
	post, err := s.loadVisiblePost(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.IncrementPostViews(r.Context(), post.ID); err == nil {
		post.ViewCount++
	}
	writeJSON(w, http.StatusOK, s.postJSONOf(r, post))
}

func (s *Server) deletePost(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	id, err := uuid.Parse(chi.URLParam(r, "postId"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if err := s.store.DeletePost(r.Context(), id, u.ID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUserPosts(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.GetUserByUsername(r.Context(), chi.URLParam(r, "username"))
	if err != nil || u.Status != "active" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	items, err := s.store.ListPostsByAuthor(r.Context(), u.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.writePostList(w, r, items)
}

func (s *Server) listChannelPosts(w http.ResponseWriter, r *http.Request) {
	ch, err := s.loadVisibleChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := s.store.ListPostsByChannel(r.Context(), ch.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.writePostList(w, r, items)
}

func (s *Server) feedPosts(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	items, err := s.store.ListFeedPosts(r.Context(), u.ID, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.writePostList(w, r, items)
}

func (s *Server) writePostList(w http.ResponseWriter, r *http.Request, items []store.Post) {
	out := make([]map[string]any, 0, len(items))
	viewer := viewerID(r)
	for _, p := range items {
		if !canSeePost(p, viewer) {
			continue
		}
		out = append(out, s.postJSONOf(r, p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) likePost(w http.ResponseWriter, r *http.Request) {
	s.setPostLike(w, r, true)
}

func (s *Server) unlikePost(w http.ResponseWriter, r *http.Request) {
	s.setPostLike(w, r, false)
}

func (s *Server) setPostLike(w http.ResponseWriter, r *http.Request, on bool) {
	u, _ := currentUser(r)
	post, err := s.loadVisiblePost(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var e error
	if on {
		e = s.store.Like(r.Context(), u.ID, "post", post.ID.String())
	} else {
		e = s.store.Unlike(r.Context(), u.ID, "post", post.ID.String())
	}
	if e != nil {
		writeErr(w, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPostComments(w http.ResponseWriter, r *http.Request) {
	post, err := s.loadVisiblePost(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := s.store.ListComments(r.Context(), "post", post.ID.String(), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commentsJSON(items)})
}

func (s *Server) createPostComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	post, err := s.loadVisiblePost(r)
	if err != nil {
		writeErr(w, err)
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
	c, err := s.store.CreateComment(r.Context(), u.ID, "post", post.ID.String(), parent, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, commentJSON(c))
}

func (s *Server) loadVisiblePost(r *http.Request) (store.Post, error) {
	id, err := uuid.Parse(chi.URLParam(r, "postId"))
	if err != nil {
		return store.Post{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	post, err := s.store.GetPost(r.Context(), id)
	if err != nil {
		return store.Post{}, err
	}
	if !canSeePost(post, viewerID(r)) {
		return store.Post{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return post, nil
}

func canSeePost(p store.Post, viewer *uuid.UUID) bool {
	if p.Channel != nil {
		return visibleTo(*p.Channel, viewer)
	}
	return p.Author.Status == "active"
}

func (s *Server) userPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "user.html", chi.URLParam(r, "username"))
}

func (s *Server) channelPage(w http.ResponseWriter, r *http.Request) {
	s.servePageEx(w, "channel.html", chi.URLParam(r, "handle"), map[string]string{"__MANAGE__": "false"})
}

func (s *Server) channelManagePage(w http.ResponseWriter, r *http.Request) {
	s.servePageEx(w, "channel.html", chi.URLParam(r, "handle"), map[string]string{"__MANAGE__": "true"})
}

func (s *Server) postPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "post.html", chi.URLParam(r, "postId"))
}

func (s *Server) homePage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "home.html", "")
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "login.html", "")
}

func (s *Server) registerPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "register.html", "")
}

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "settings.html", "")
}

func (s *Server) composePage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "compose.html", "")
}

func (s *Server) feedPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "feed.html", "")
}

const staticAssetVersion = "20260923j"

func (s *Server) servePage(w http.ResponseWriter, file, slot string) {
	s.servePageEx(w, file, slot, nil)
}

func (s *Server) servePageEx(w http.ResponseWriter, file, slot string, extra map[string]string) {
	body, err := watchHTML.ReadFile(file)
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)
		return
	}
	enc, _ := json.Marshal(slot)
	page := strings.ReplaceAll(string(body), `"__SLOT__"`, string(enc))
	for k, v := range extra {
		page = strings.ReplaceAll(page, k, v)
	}
	if !strings.Contains(page, "/static/token.js") {
		page = strings.Replace(page, "</head>", `<script src="/static/token.js"></script>
</head>`, 1)
	}
	for _, name := range []string{"account.js", "account.css", "token.js", "media-edge.js", "post-media.js", "post-media.css", "player.js", "player.css", "shorts-engine.js", "shorts-worker.js", "shorts-decode-worker.js", "shorts-worklet.js", "shorts-hwtest.js"} {
		page = strings.ReplaceAll(page, "/static/"+name, "/static/"+name+"?v="+staticAssetVersion)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

func (s *Server) serveAsset(file, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := watchHTML.ReadFile(file)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("CDN-Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}
