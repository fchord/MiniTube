package httpapi

import (
	"embed"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"minitube/api/internal/ident"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
	"minitube/api/internal/transcode"
)

//go:embed watch.html user.html channel.html post.html home.html compose.html feed.html shorts.html shorts-hwtest.html live.html lives.html login.html register.html settings.html admin.html admin-setup.html account.css account.js token.js media-edge.js post-media.css post-media.js publish.html player.css player.js shorts-engine.js shorts-hwtest.js shorts-worker.js shorts-decode-worker.js shorts-worklet.js
var watchHTML embed.FS

type createVideoReq struct {
	ChannelID   string `json:"channelId"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	Visibility  string `json:"visibility"`
	MimeType    string `json:"mimeType"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
}

type updateVideoReq struct {
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	Status      *string    `json:"status"`
	Visibility  *string    `json:"visibility"`
	ScheduledAt *time.Time `json:"scheduledAt"`
}

func mediaCacheBust(u string, t time.Time) string {
	if u == "" || t.IsZero() {
		return u
	}
	v := strconv.FormatInt(t.UTC().Unix(), 10)
	if strings.Contains(u, "?") {
		return u + "&v=" + v
	}
	return u + "?v=" + v
}

func mediaCacheBustPtr(u *string, t time.Time) any {
	if u == nil || *u == "" {
		return nil
	}
	return mediaCacheBust(*u, t)
}

func videoJSON(v store.Video, liked bool) map[string]any {
	pub := any(nil)
	if v.PublishedAt != nil {
		pub = v.PublishedAt.UTC().Format(time.RFC3339)
	}
	out := map[string]any{
		"id":           v.ID,
		"channel":      channelJSONOf(v.Channel, nil),
		"kind":         v.Kind,
		"title":        v.Title,
		"description":  v.Description,
		"status":       v.Status,
		"visibility":   v.Visibility,
		"durationMs":   v.DurationMs,
		"width":        v.Width,
		"height":       v.Height,
		"aspectRatio":  v.AspectRatio,
		"thumbnailUrl": mediaCacheBustPtr(v.ThumbnailURL, v.UpdatedAt),
		"viewCount":    v.ViewCount,
		"likeCount":    v.LikeCount,
		"commentCount": v.CommentCount,
		"liked":        liked,
		"errorMessage": v.ErrorMessage,
		"publishedAt":  pub,
		"createdAt":    v.CreatedAt.UTC().Format(time.RFC3339),
	}
	if v.SourceBytes != nil && *v.SourceBytes > 0 {
		out["sizeBytes"] = *v.SourceBytes
		out["sizeLabel"] = formatSourceSize(*v.SourceBytes)
	}
	return out
}

func formatSourceSize(bytes int64) string {
	if bytes < 0 {
		return ""
	}
	const gb = 1 << 30
	const mb = 1 << 20
	unit := "MB"
	n := float64(bytes) / float64(mb)
	if bytes >= gb {
		unit = "GB"
		n = float64(bytes) / float64(gb)
	}
	if n == 0 {
		return "0" + unit
	}
	exp := math.Floor(math.Log10(math.Abs(n)))
	pow := math.Pow(10, exp-1)
	n = math.Round(n/pow) * pow
	prec := int(1 - math.Floor(math.Log10(math.Abs(n))))
	if prec < 0 {
		prec = 0
	}
	return strconv.FormatFloat(n, 'f', prec, 64) + unit
}

func (s *Server) hydrateSize(v *store.Video) {
	if v.SourceBytes != nil && *v.SourceBytes > 0 {
		return
	}
	if v.SourceObjectKey != nil && *v.SourceObjectKey != "" {
		if n, ok := s.uploads.FileSize(*v.SourceObjectKey); ok && n > 0 {
			v.SourceBytes = &n
		}
	}
}

func (s *Server) videoJSONOf(v store.Video, liked bool) map[string]any {
	s.hydrateSize(&v)
	return videoJSON(v, liked)
}

func (s *Server) createVideo(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	var req createVideoReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	kind := req.Kind
	if kind == "" {
		kind = "long"
	}
	if kind != "long" && kind != "short" {
		writeError(w, http.StatusBadRequest, "bad_request", "kind must be long or short")
		return
	}
	vis := req.Visibility
	if vis == "" {
		vis = "public"
	}
	if vis != "public" && vis != "private" {
		writeError(w, http.StatusBadRequest, "bad_request", "visibility must be public or private")
		return
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(req.Title)); n < 1 || n > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "title required")
		return
	}
	if req.Size < 1 {
		writeError(w, http.StatusBadRequest, "bad_request", "size required")
		return
	}
	if req.Size > storage.MaxVideoBytes {
		writeError(w, http.StatusBadRequest, "bad_request", "视频不能超过 5GB")
		return
	}
	chID, err := uuid.Parse(req.ChannelID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid channelId")
		return
	}
	ch, err := s.store.GetChannelByID(r.Context(), chID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ch.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "not your channel")
		return
	}
	busy, err := s.store.OwnerHasProcessing(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if busy {
		writeError(w, http.StatusConflict, "conflict", "已有视频正在转码")
		return
	}
	stale, err := s.store.DeleteOwnerUploading(r.Context(), u.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	for _, id := range stale {
		s.uploads.AbortVideoTmp(id)
	}
	v, err := s.store.CreateVideo(r.Context(), ch.ID, kind, strings.TrimSpace(req.Title), req.Description, vis, "", req.Size)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.uploads.BeginVideoChunks(u.ID.String(), v.ID, req.MimeType, req.Filename, req.Size); err != nil {
		_ = s.store.DeleteVideo(r.Context(), v.ID)
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v.Channel = ch
	writeJSON(w, http.StatusCreated, map[string]any{
		"video":  s.videoJSONOf(v, false),
		"upload": map[string]any{"chunkSize": storage.VideoChunkSize, "size": req.Size},
	})
}

func (s *Server) putVideoChunk(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if v.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if v.Status != "uploading" {
		writeError(w, http.StatusConflict, "conflict", "not uploading")
		return
	}
	idx, err := strconv.Atoi(chi.URLParam(r, "index"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid chunk index")
		return
	}
	if err := s.uploads.PutChunk(u.ID.String(), v.ID, idx, io.LimitReader(r.Body, storage.VideoChunkSize+1)); err != nil {
		if storage.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", "upload expired")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"index": idx})
}

func (s *Server) getVideo(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := videoAccessErr(v, viewerID(r)); err != nil {
		writeErr(w, err)
		return
	}
	liked := false
	if vid := viewerID(r); vid != nil {
		liked, _ = s.store.Liked(r.Context(), *vid, v.ID)
	}
	writeJSON(w, http.StatusOK, s.videoPayload(v, liked))
}

func (s *Server) videoPayload(v store.Video, liked bool) map[string]any {
	out := s.videoJSONOf(v, liked)
	out["transcode"] = transcode.InspectProgress(s.uploads, v).Map()
	return out
}

func (s *Server) patchVideo(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if v.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	var req updateVideoReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Status != nil && *req.Status != "ready" {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid status")
		return
	}
	if req.Status != nil && *req.Status == "ready" && v.Status != "ready" {
		writeError(w, http.StatusConflict, "conflict", "not transcoded")
		return
	}
	if req.Visibility != nil {
		if *req.Visibility != "public" && *req.Visibility != "private" {
			writeError(w, http.StatusBadRequest, "bad_request", "visibility must be public or private")
			return
		}
		if v.Visibility == "deleted" {
			writeError(w, http.StatusNotFound, "not_found", "not found")
			return
		}
	}
	updated := v
	if req.Title != nil || req.Description != nil || req.Status != nil || req.ScheduledAt != nil {
		var err error
		updated, err = s.store.UpdateVideoMeta(r.Context(), v.ID, req.Title, req.Description, req.Status, req.ScheduledAt)
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	if req.Visibility != nil {
		var err error
		updated, err = s.store.SetVideoVisibility(r.Context(), v.ID, *req.Visibility)
		if err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, s.videoJSONOf(updated, false))
}

func (s *Server) deleteVideo(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if v.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if r.URL.Query().Get("purge") == "1" {
		if err := s.purgeVideo(r.Context(), v); err != nil {
			writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if v.Visibility == "deleted" {
		writeError(w, http.StatusNotFound, "not_found", "not found")
		return
	}
	if _, err := s.store.SetVideoVisibility(r.Context(), v.ID, "deleted"); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if v.Channel.OwnerUserID != u.ID {
		writeError(w, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	key, err := s.uploads.MergeVideo(v.ID)
	if err != nil {
		if storage.IsNotFound(err) {
			writeError(w, http.StatusBadRequest, "bad_request", "source not uploaded")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.store.SetSourceKey(r.Context(), v.ID, key); err != nil {
		writeErr(w, err)
		return
	}
	updated, err := s.store.MarkProcessing(r.Context(), v.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, s.videoPayload(updated, false))
}

func (s *Server) getPlayback(w http.ResponseWriter, r *http.Request) {
	v, err := s.loadVideo(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := videoAccessErr(v, viewerID(r)); err != nil {
		writeErr(w, err)
		return
	}
	if v.Status != "ready" {
		writeError(w, http.StatusConflict, "not_ready", "not ready")
		return
	}
	rends, err := s.store.ListRenditions(r.Context(), v.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	aud, err := s.store.ListAudioTracks(r.Context(), v.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	subs, err := s.store.ListSubtitleTracks(r.Context(), v.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	masterKey := "hls/" + v.ID + "/master.m3u8"
	if !s.uploads.Wait(r.Context(), masterKey, 8*time.Second) {
		writeError(w, http.StatusConflict, "not_ready", "not ready")
		return
	}
	_ = s.store.IncrementViews(r.Context(), v.ID)
	rendJSON := make([]map[string]any, 0, len(rends))
	for _, x := range rends {
		rendJSON = append(rendJSON, map[string]any{
			"height": x.Height, "bandwidthBps": x.BandwidthBps,
			"playlistUrl": mediaCacheBust(s.uploads.URL(x.PlaylistKey), v.UpdatedAt), "codec": x.Codec,
		})
	}
	aJSON := make([]map[string]any, 0, len(aud))
	for _, x := range aud {
		aJSON = append(aJSON, map[string]any{
			"language": x.Language, "label": x.Label, "isDefault": x.IsDefault,
			"playlistUrl": mediaCacheBust(s.uploads.URL(x.PlaylistKey), v.UpdatedAt),
		})
	}
	sJSON := make([]map[string]any, 0, len(subs))
	for _, x := range subs {
		sJSON = append(sJSON, map[string]any{
			"language": x.Language, "label": x.Label, "isDefault": x.IsDefault,
			"vttUrl": s.uploads.URL(x.VTTKey),
		})
	}
	out := map[string]any{
		"masterPlaylistUrl": mediaCacheBust(s.uploads.URL("hls/"+v.ID+"/master.m3u8"), v.UpdatedAt),
		"renditions":        rendJSON,
		"audioTracks":       aJSON,
		"subtitleTracks":    sJSON,
	}
	s.rewriteMediaField(r, out, "masterPlaylistUrl")
	s.rewriteMediaURLList(r, rendJSON, "playlistUrl")
	s.rewriteMediaURLList(r, aJSON, "playlistUrl")
	s.rewriteMediaURLList(r, sJSON, "vttUrl")
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) publishPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "publish.html", "")
}

func (s *Server) watchPage(w http.ResponseWriter, r *http.Request) {
	s.servePageEx(w, "watch.html", "", map[string]string{
		"__VIDEO_ID__": chi.URLParam(r, "videoId"),
	})
}

func (s *Server) shortsPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "shorts.html", chi.URLParam(r, "videoId"))
}

func (s *Server) shortsHwtestPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "shorts-hwtest.html", "")
}

func (s *Server) feedShorts(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r)
	if r.URL.Query().Get("random") == "1" {
		s.feedShortsRandom(w, r, limit)
		return
	}
	t, id, ok := decodeCursor(r.URL.Query().Get("cursor"))
	if !ok {
		t, id = time.Time{}, ""
	}
	items, err := s.store.ListFeedShorts(r.Context(), t, id, limit+1)
	if err != nil {
		writeErr(w, err)
		return
	}
	var next any
	if len(items) > limit {
		last := items[limit-1]
		ct := last.CreatedAt
		if last.PublishedAt != nil {
			ct = *last.PublishedAt
		}
		next = encodeCursor(ct, last.ID)
		items = items[:limit]
	}
	s.writeShortsFeed(w, r, items, next)
}

func (s *Server) feedShortsRandom(w http.ResponseWriter, r *http.Request, limit int) {
	exclude := parseVideoIDList(r.URL.Query().Get("exclude"), 200)
	items, err := s.store.ListRandomFeedShorts(r.Context(), exclude, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	next := any(nil)
	if len(items) == limit {
		next = "more"
	}
	s.writeShortsFeed(w, r, items, next)
}

func parseVideoIDList(s string, max int) []string {
	if s == "" || max < 1 {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(out) >= max {
			break
		}
		id := strings.TrimSpace(p)
		if !ident.ValidVideoID(id) {
			continue
		}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *Server) writeShortsFeed(w http.ResponseWriter, r *http.Request, items []store.Video, next any) {
	viewer := viewerID(r)
	out := make([]map[string]any, 0, len(items))
	for _, v := range items {
		if err := videoAccessErr(v, viewer); err != nil {
			continue
		}
		liked := false
		if viewer != nil {
			liked, _ = s.store.Liked(r.Context(), *viewer, v.ID)
		}
		out = append(out, s.videoJSONOf(v, liked))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "nextCursor": next})
}

func (s *Server) loadVideo(r *http.Request) (store.Video, error) {
	id, err := s.store.ResolveVideoID(r.Context(), chi.URLParam(r, "videoId"))
	if err != nil {
		return store.Video{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	v, err := s.store.GetVideo(r.Context(), id)
	if err != nil {
		return store.Video{}, err
	}
	return v, nil
}

func videoAccessErr(v store.Video, viewer *uuid.UUID) error {
	if v.Visibility == "deleted" {
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	owner := viewer != nil && *viewer == v.Channel.OwnerUserID
	if owner {
		return nil
	}
	if v.Status != "ready" {
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	if v.Visibility == "private" {
		return apiError{status: http.StatusForbidden, code: "private", message: "该视频为私藏，无法观看"}
	}
	if !visibleTo(v.Channel, viewer) {
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return nil
}

func canSeeVideo(v store.Video, viewer *uuid.UUID) bool {
	return videoAccessErr(v, viewer) == nil
}

func requireVerified(u store.User) error {
	if u.EmailVerifiedAt == nil {
		return apiError{status: http.StatusForbidden, code: "forbidden", message: "email not verified"}
	}
	return nil
}

func (s *Server) listChannelVideosReal(w http.ResponseWriter, r *http.Request) {
	ch, err := s.loadVisibleChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind == "" {
		kind = "long"
	}
	if kind != "long" && kind != "short" {
		writeError(w, http.StatusBadRequest, "bad_request", "kind must be long or short")
		return
	}
	viewer := viewerID(r)
	include := viewer != nil && *viewer == ch.OwnerUserID
	items, err := s.store.ListChannelVideos(r.Context(), ch.ID, kind, include, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, v := range items {
		v.Channel = ch
		if err := videoAccessErr(v, viewer); err != nil {
			continue
		}
		liked := false
		if viewer != nil {
			liked, _ = s.store.Liked(r.Context(), *viewer, v.ID)
		}
		out = append(out, s.videoJSONOf(v, liked))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}
