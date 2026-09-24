package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"minitube/api/internal/store"
)

type chatHub struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[chan map[string]any]struct{}
}

func newChatHub() *chatHub {
	return &chatHub{subs: map[uuid.UUID]map[chan map[string]any]struct{}{}}
}

func (h *chatHub) subscribe(id uuid.UUID) chan map[string]any {
	ch := make(chan map[string]any, 16)
	h.mu.Lock()
	if h.subs[id] == nil {
		h.subs[id] = map[chan map[string]any]struct{}{}
	}
	h.subs[id][ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *chatHub) unsubscribe(id uuid.UUID, ch chan map[string]any) {
	h.mu.Lock()
	delete(h.subs[id], ch)
	if len(h.subs[id]) == 0 {
		delete(h.subs, id)
	}
	h.mu.Unlock()
	close(ch)
}

func (h *chatHub) broadcast(id uuid.UUID, msg map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[id] {
		select {
		case ch <- msg:
		default:
		}
	}
}

type createLiveReq struct {
	ChannelID   string     `json:"channelId"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	ScheduledAt *time.Time `json:"scheduledAt"`
}

func liveJSON(l store.Live, playback string, liked bool, ingest map[string]string) map[string]any {
	var vod any
	if l.VODVideoID != nil {
		vod = *l.VODVideoID
	}
	var started, ended, scheduled any
	if l.StartedAt != nil {
		started = l.StartedAt.UTC().Format(time.RFC3339)
	}
	if l.EndedAt != nil {
		ended = l.EndedAt.UTC().Format(time.RFC3339)
	}
	if l.ScheduledAt != nil {
		scheduled = l.ScheduledAt.UTC().Format(time.RFC3339)
	}
	showPB := l.Status == "live" || l.Status == "ended"
	var pb any
	if playback != "" && showPB {
		pb = playback
	} else if l.PlaybackURL != nil && showPB {
		pb = *l.PlaybackURL
	}
	out := map[string]any{
		"id":           l.ID.String(),
		"channel":      channelJSONOf(l.Channel, nil),
		"title":        l.Title,
		"description":  l.Description,
		"status":       l.Status,
		"visibility":   l.Visibility,
		"playbackUrl":  pb,
		"thumbnailUrl": l.ThumbnailURL,
		"vodVideoId":   vod,
		"likeCount":    l.LikeCount,
		"liked":        liked,
		"viewerCount":  l.ViewerCount,
		"scheduledAt":  scheduled,
		"startedAt":    started,
		"endedAt":      ended,
		"createdAt":    l.CreatedAt.UTC().Format(time.RFC3339),
	}
	if ingest != nil {
		out["ingestUrl"] = ingest["url"]
		out["ingestKey"] = ingest["key"]
	}
	if l.Status == "ended" && l.ArchiveBytes != nil && *l.ArchiveBytes > 0 {
		out["sizeBytes"] = *l.ArchiveBytes
		out["sizeLabel"] = formatSourceSize(*l.ArchiveBytes)
	}
	return out
}

func (s *Server) liveJSONOf(r *http.Request, l store.Live, ingest map[string]string) map[string]any {
	liked := false
	if vid := viewerID(r); vid != nil {
		liked, _ = s.store.LiveLiked(r.Context(), *vid, l.ID)
	}
	pb := ""
	if s.live != nil {
		pb = s.live.PlaybackURL(l.ID)
	}
	out := liveJSON(l, pb, liked, ingest)
	s.rewriteMediaField(r, out, "playbackUrl")
	return out
}

func (s *Server) createLive(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	var req createLiveReq
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(req.Title)); n < 1 || n > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "title required")
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
	key, err := randomLiveKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "internal error")
		return
	}
	ingestURL := strings.TrimRight(s.cfg.RTMPBaseURL, "/") + "/" + key
	live, err := s.store.CreateLive(r.Context(), ch.ID, strings.TrimSpace(req.Title), strings.TrimSpace(req.Description), req.ScheduledAt, key, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, s.liveJSONOf(r, live, map[string]string{"url": ingestURL, "key": key}))
}

func (s *Server) getLive(w http.ResponseWriter, r *http.Request) {
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.liveJSONOf(r, live, nil))
}

func (s *Server) listPublicLive(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPublicLive(r.Context(), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.writeLiveList(w, r, items)
}

func (s *Server) listChannelLive(w http.ResponseWriter, r *http.Request) {
	ch, err := s.loadVisibleChannel(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	viewer := viewerID(r)
	ownerView := viewer != nil && *viewer == ch.OwnerUserID
	items, err := s.store.ListChannelLives(r.Context(), ch.ID, ownerView, parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	s.writeLiveList(w, r, items)
}

func (s *Server) writeLiveList(w http.ResponseWriter, r *http.Request, items []store.Live) {
	out := make([]map[string]any, 0, len(items))
	viewer := viewerID(r)
	for _, l := range items {
		if !canSeeLive(l, viewer) {
			continue
		}
		out = append(out, s.liveJSONOf(r, l, nil))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) broadcastLiveEnded(live store.Live) {
	var vod any
	if live.VODVideoID != nil {
		vod = *live.VODVideoID
	}
	s.hub.broadcast(live.ID, map[string]any{
		"type":       "status",
		"status":     live.Status,
		"vodVideoId": vod,
	})
}

func (s *Server) endLive(w http.ResponseWriter, r *http.Request) {
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
	if s.live != nil {
		if err := s.live.End(r.Context(), live.ID); err != nil {
			writeErr(w, err)
			return
		}
	} else if err := s.store.MarkLiveEnded(r.Context(), live.ID); err != nil {
		writeErr(w, err)
		return
	}
	live, err = s.store.GetLive(r.Context(), live.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.liveJSONOf(r, live, nil))
}

func (s *Server) livePlayback(w http.ResponseWriter, r *http.Request) {
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if live.Status == "scheduled" {
		writeError(w, http.StatusConflict, "not_ready", "not live")
		return
	}
	if live.Status == "ended" && (s.live == nil || live.IngestKey == "" || !s.live.HasArchive(live.IngestKey)) {
		writeError(w, http.StatusConflict, "not_ready", "no recording")
		return
	}
	url := ""
	if s.live != nil {
		url = s.live.PlaybackURL(live.ID)
	}
	out := map[string]any{"masterPlaylistUrl": s.sameOriginMediaURL(r, url)}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) liveHLS(w http.ResponseWriter, r *http.Request) {
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if live.IngestKey == "" || s.live == nil {
		writeError(w, http.StatusConflict, "not_ready", "origin unavailable")
		return
	}
	rest := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	s.live.ServeHLS(w, r, live, rest)
}

func (s *Server) likeLive(w http.ResponseWriter, r *http.Request) {
	s.setLiveLike(w, r, true)
}

func (s *Server) unlikeLive(w http.ResponseWriter, r *http.Request) {
	s.setLiveLike(w, r, false)
}

func (s *Server) setLiveLike(w http.ResponseWriter, r *http.Request, on bool) {
	u, _ := currentUser(r)
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var e error
	if on {
		e = s.store.Like(r.Context(), u.ID, "live", live.ID.String())
	} else {
		e = s.store.Unlike(r.Context(), u.ID, "live", live.ID.String())
	}
	if e != nil {
		writeErr(w, e)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listLiveComments(w http.ResponseWriter, r *http.Request) {
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	items, err := s.store.ListComments(r.Context(), "live", live.ID.String(), parseLimit(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": commentsJSON(items)})
}

func (s *Server) createLiveComment(w http.ResponseWriter, r *http.Request) {
	u, _ := currentUser(r)
	if err := requireVerified(u); err != nil {
		writeErr(w, err)
		return
	}
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if live.Status == "ended" {
		writeError(w, http.StatusConflict, "ended", "live ended")
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
	c, err := s.store.CreateComment(r.Context(), u.ID, "live", live.ID.String(), nil, body)
	if err != nil {
		writeErr(w, err)
		return
	}
	msg := commentJSON(c)
	s.hub.broadcast(live.ID, msg)
	writeJSON(w, http.StatusCreated, msg)
}

func (s *Server) liveChatStream(w http.ResponseWriter, r *http.Request) {
	live, err := s.loadVisibleLive(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal", "stream unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl.Flush()
	ch := s.hub.subscribe(live.ID)
	defer s.hub.unsubscribe(live.ID, ch)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			fl.Flush()
		case msg, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(msg)
			_, _ = io.WriteString(w, "data: "+string(b)+"\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) livePage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "live.html", chi.URLParam(r, "liveId"))
}

func (s *Server) livesPage(w http.ResponseWriter, r *http.Request) {
	s.servePage(w, "lives.html", "")
}

func (s *Server) loadLive(r *http.Request) (store.Live, error) {
	id, err := uuid.Parse(chi.URLParam(r, "liveId"))
	if err != nil {
		return store.Live{}, apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return s.store.GetLive(r.Context(), id)
}

func (s *Server) loadVisibleLive(r *http.Request) (store.Live, error) {
	live, err := s.loadLive(r)
	if err != nil {
		return store.Live{}, err
	}
	if err := liveAccessErr(live, viewerID(r)); err != nil {
		return store.Live{}, err
	}
	return live, nil
}

func liveAccessErr(l store.Live, viewer *uuid.UUID) error {
	if l.Visibility == "deleted" {
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	owner := viewer != nil && *viewer == l.Channel.OwnerUserID
	if owner {
		return nil
	}
	if l.Visibility == "private" {
		return apiError{status: http.StatusForbidden, code: "private", message: "该直播为私藏，无法观看"}
	}
	if !visibleTo(l.Channel, viewer) {
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return nil
}

func canSeeLive(l store.Live, viewer *uuid.UUID) bool {
	return liveAccessErr(l, viewer) == nil
}

func randomLiveKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
