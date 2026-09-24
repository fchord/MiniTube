package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"minitube/api/internal/auth"
	"minitube/api/internal/config"
	"minitube/api/internal/liveorigin"
	"minitube/api/internal/mailer"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

type Server struct {
	cfg     config.Config
	store   *store.Postgres
	jwt     *auth.JWT
	mail    mailer.Sender
	uploads *storage.Local
	live    *liveorigin.Engine
	hub     *chatHub
	handler http.Handler

	adminMu   sync.Mutex
	acct      *adminAccount
	adminTok  map[string]time.Time
	setupTok  map[string]time.Time
	adminFail map[string]*adminLoginState
	nowFn     func() time.Time

	edgeHostsMu sync.Mutex
	edgeHosts   []string
}

func New(cfg config.Config, st *store.Postgres, jwt *auth.JWT, mail mailer.Sender, uploads *storage.Local) *Server {
	s := &Server{
		cfg: cfg, store: st, jwt: jwt, mail: mail, uploads: uploads,
		live: liveorigin.New(cfg, st, uploads),
		hub:  newChatHub(),
	}
	s.live.OnEnded = s.broadcastLiveEnded
	if err := s.EnsureAdmin(context.Background()); err != nil {
		slog.Error("ensure admin", "err", err)
	}
	s.refreshEdgeHosts(context.Background())
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "HEAD", "POST", "PATCH", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Range"},
		ExposedHeaders:   []string{"Link", "Content-Range", "Accept-Ranges"},
		AllowCredentials: false,
		MaxAge:           300,
	}))
	r.Use(s.edgeHostGuard)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/", s.homePage)
	r.Get("/login", s.loginPage)
	r.Get("/register", s.registerPage)
	r.Get("/settings", s.settingsPage)
	r.With(s.requireSetupIntranet).Get("/admin/setup", s.adminSetupPage)
	r.Get("/admin", s.adminPage)
	r.Get("/compose", s.composePage)
	r.Get("/feed", s.feedPage)
	r.Get("/static/post-media.css", s.serveAsset("post-media.css", "text/css; charset=utf-8"))
	r.Get("/static/post-media.js", s.serveAsset("post-media.js", "text/javascript; charset=utf-8"))
	r.Get("/static/account.css", s.serveAsset("account.css", "text/css; charset=utf-8"))
	r.Get("/static/account.js", s.serveAsset("account.js", "text/javascript; charset=utf-8"))
	r.Get("/static/token.js", s.serveAsset("token.js", "text/javascript; charset=utf-8"))
	r.Get("/static/media-edge.js", s.serveAsset("media-edge.js", "text/javascript; charset=utf-8"))
	r.Get("/static/player.css", s.serveAsset("player.css", "text/css; charset=utf-8"))
	r.Get("/static/player.js", s.serveAsset("player.js", "text/javascript; charset=utf-8"))
	r.Get("/static/shorts-engine.js", s.serveAsset("shorts-engine.js", "text/javascript; charset=utf-8"))
	r.Get("/static/shorts-worker.js", s.serveAsset("shorts-worker.js", "text/javascript; charset=utf-8"))
	r.Get("/static/shorts-decode-worker.js", s.serveAsset("shorts-decode-worker.js", "text/javascript; charset=utf-8"))
	r.Get("/static/shorts-worklet.js", s.serveAsset("shorts-worklet.js", "text/javascript; charset=utf-8"))
	r.Get("/static/shorts-hwtest.js", s.serveAsset("shorts-hwtest.js", "text/javascript; charset=utf-8"))
	r.Get("/shorts/hwtest", s.shortsHwtestPage)
	r.Get("/shorts", s.shortsPage)
	r.Get("/shorts/{videoId}", s.shortsPage)
	r.Get("/watch/{videoId}", s.watchPage)
	r.Get("/live", s.livesPage)
	r.Get("/live/{liveId}", s.livePage)
	r.Get("/u/{username}", s.userPage)
	r.Get("/c/{handle}", s.channelPage)
	r.Get("/c/{handle}/manage", s.channelManagePage)
	r.Get("/publish", s.publishPage)
	r.Get("/post/{postId}", s.postPage)

	r.Route("/v1", func(r chi.Router) {
		r.Post("/auth/register", s.register)
		r.Post("/auth/login", s.login)
		r.Post("/auth/verify-email", s.verifyEmail)
		r.With(s.requireAuth).Post("/auth/logout", s.logout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/me", s.getMe)
			r.Patch("/me", s.patchMe)
			r.Post("/me/avatar", s.avatar)
			r.Put("/me/password", s.changePassword)
			r.Get("/me/subscriptions", s.mySubscriptions)
			r.Get("/me/watch-history", s.listWatchHistory)
			r.Get("/me/comments", s.listMyCommentsReal)
			r.Get("/me/favorites", s.myFavorites)
			r.Post("/channels", s.createChannel)
			r.Post("/videos", s.createVideo)
		})

		r.With(s.optionalAuth).Get("/users/{username}", s.getUser)
		r.With(s.optionalAuth).Get("/users/{username}/channels", s.listUserChannels)

		r.With(s.optionalAuth).Get("/channels/{handle}", s.getChannel)
		r.With(s.requireAuth).Patch("/channels/{handle}", s.patchChannel)
		r.With(s.optionalAuth).Get("/channels/{handle}/videos", s.listChannelVideosReal)
		r.With(s.requireAuth).Get("/channels/{handle}/trash", s.listChannelTrash)
		r.With(s.requireAuth).Post("/channels/{handle}/manage", s.manageChannel)
		r.With(s.requireAuth).Post("/channels/{handle}/subscribe", s.subscribe)
		r.With(s.requireAuth).Delete("/channels/{handle}/subscribe", s.unsubscribe)

		r.Put("/uploads/{token}", s.putUpload)
		r.Get("/media/edge-probe", s.mediaEdgeProbe)
		r.With(s.optionalAuth).Get("/media/*", s.serveMedia)

		r.With(s.requireAuth).Get("/favorites/{listId}/items", s.listFavoriteItems)
		r.With(s.requireAuth).Post("/favorites/{listId}/items", s.addFavoriteItem)
		r.With(s.requireAuth).Delete("/favorites/{listId}/items/{targetType}/{targetId}", s.removeFavoriteItem)

		r.With(s.optionalAuth).Get("/videos/{videoId}", s.getVideo)
		r.With(s.requireAuth).Patch("/videos/{videoId}", s.patchVideo)
		r.With(s.requireAuth).Delete("/videos/{videoId}", s.deleteVideo)
		r.With(s.requireAuth).Post("/videos/{videoId}/complete-upload", s.completeUpload)
		r.With(s.requireAuth).Put("/videos/{videoId}/chunks/{index}", s.putVideoChunk)
		r.With(s.optionalAuth).Get("/videos/{videoId}/playback", s.getPlayback)
		r.With(s.requireAuth).Put("/videos/{videoId}/progress", s.putProgress)
		r.With(s.requireAuth).Post("/videos/{videoId}/like", s.likeVideo)
		r.With(s.requireAuth).Delete("/videos/{videoId}/like", s.unlikeVideo)
		r.With(s.optionalAuth).Get("/videos/{videoId}/comments", s.listVideoComments)
		r.With(s.requireAuth).Post("/videos/{videoId}/comments", s.createVideoComment)

		r.Get("/public/site", s.getPublicSite)
		r.Get("/admin/login-status", s.adminLoginLockStatus)
		r.Post("/admin/login", s.adminLogin)
		r.Group(func(r chi.Router) {
			r.Use(s.requireSetupIntranet)
			r.Get("/admin/setup", s.adminSetupStatus)
			r.Post("/admin/setup/login", s.adminSetupLogin)
			r.With(s.requireSetupAuth).Post("/admin/setup/password", s.adminSetupPassword)
			r.With(s.requireSetupAuth).Post("/admin/setup/totp/begin", s.adminSetupTOTPBegin)
			r.With(s.requireSetupAuth).Post("/admin/setup/totp/confirm", s.adminSetupTOTPConfirm)
		})
		r.With(s.requireSiteOrUserAdmin).Get("/admin/site", s.getPublicSite)
		r.With(s.requireSiteOrUserAdmin).Patch("/admin/site", s.patchAdminSite)

		r.With(s.requireAuth).Post("/posts", s.createPost)
		r.With(s.requireAuth).Post("/posts/images", s.beginPostImage)
		r.With(s.optionalAuth).Get("/posts/{postId}", s.getPost)
		r.With(s.requireAuth).Delete("/posts/{postId}", s.deletePost)
		r.With(s.requireAuth).Post("/posts/{postId}/like", s.likePost)
		r.With(s.requireAuth).Delete("/posts/{postId}/like", s.unlikePost)
		r.With(s.optionalAuth).Get("/posts/{postId}/comments", s.listPostComments)
		r.With(s.requireAuth).Post("/posts/{postId}/comments", s.createPostComment)
		r.With(s.optionalAuth).Get("/users/{username}/posts", s.listUserPosts)
		r.With(s.optionalAuth).Get("/channels/{handle}/posts", s.listChannelPosts)
		r.With(s.requireAuth).Get("/feed/posts", s.feedPosts)

		r.With(s.optionalAuth).Get("/feed/shorts", s.feedShorts)

		r.With(s.requireAuth).Post("/live", s.createLive)
		r.With(s.optionalAuth).Get("/live", s.listPublicLive)
		r.With(s.optionalAuth).Get("/live/{liveId}", s.getLive)
		r.With(s.requireAuth).Patch("/live/{liveId}", s.patchLive)
		r.With(s.requireAuth).Delete("/live/{liveId}", s.deleteLive)
		r.With(s.requireAuth).Post("/live/{liveId}/end", s.endLive)
		r.With(s.optionalAuth).Get("/live/{liveId}/playback", s.livePlayback)
		r.With(s.optionalAuth).Get("/live/{liveId}/hls/*", s.liveHLS)
		r.With(s.requireAuth).Post("/live/{liveId}/like", s.likeLive)
		r.With(s.requireAuth).Delete("/live/{liveId}/like", s.unlikeLive)
		r.With(s.optionalAuth).Get("/live/{liveId}/comments", s.listLiveComments)
		r.With(s.requireAuth).Post("/live/{liveId}/comments", s.createLiveComment)
		r.With(s.optionalAuth).Get("/live/{liveId}/chat/stream", s.liveChatStream)
		r.With(s.optionalAuth).Get("/channels/{handle}/live", s.listChannelLive)

		r.Post("/internal/srs/on_publish", s.srsOnPublish)
		r.Post("/internal/srs/on_unpublish", s.srsOnUnpublish)
		r.Post("/internal/srs/on_hls", s.srsOnHLS)
	})
	s.handler = r
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) RunLivePoll(ctx context.Context) {
	if s.live != nil {
		s.live.Run(ctx)
	}
}

func (s *Server) putUpload(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	ct := r.Header.Get("Content-Type")
	if err := s.uploads.Put(token, r.Body, ct); err != nil {
		if storage.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "not_found", "upload token expired")
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(chi.URLParam(r, "*"), "/")
	if err := s.mediaAccessErr(r, key); err != nil {
		writeErr(w, err)
		return
	}
	if kind, _, ok := mediaVideoRef(key); ok && (kind == "hls" || kind == "sources") {
		if kind == "hls" && strings.HasSuffix(strings.ToLower(key), ".m3u8") {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("CDN-Cache-Control", "no-store")
			if !s.uploads.Exists(key) {
				s.uploads.Wait(r.Context(), key, 8*time.Second)
			}
		} else if kind == "hls" {
			w.Header().Set("Cache-Control", "public, max-age=86400")
		} else {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("CDN-Cache-Control", "no-store")
		}
	}
	s.uploads.Serve(w, r, key)
}

func mediaVideoRef(key string) (kind string, id string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(key, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	if parts[0] != "hls" && parts[0] != "sources" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Server) mediaAccessErr(r *http.Request, key string) error {
	kind, raw, ok := mediaVideoRef(key)
	if !ok {
		return nil
	}
	id, err := s.store.ResolveVideoID(r.Context(), raw)
	if err != nil {
		return err
	}
	v, err := s.store.GetVideo(r.Context(), id)
	if err != nil {
		return err
	}
	vid := viewerID(r)
	owner := vid != nil && *vid == v.Channel.OwnerUserID
	if kind == "sources" {
		if !owner {
			return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
		}
		return nil
	}
	if v.Visibility == "deleted" {
		if owner {
			return nil
		}
		return apiError{status: http.StatusNotFound, code: "not_found", message: "not found"}
	}
	return videoAccessErr(v, vid)
}
