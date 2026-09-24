package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"minitube/api/internal/auth"
	"minitube/api/internal/config"
	"minitube/api/internal/db"
	"minitube/api/internal/edgetls"
	"minitube/api/internal/httpapi"
	"minitube/api/internal/mailer"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.FromEnv()
	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		logger.Error("migrate", "err", err)
		os.Exit(1)
	}
	if err := db.RemapVideoMedia(ctx, pool, cfg.UploadDir); err != nil {
		logger.Error("remap video media", "err", err)
		os.Exit(1)
	}

	uploads, err := storage.NewLocal(cfg.UploadDir, cfg.PublicBaseURL)
	if err != nil {
		logger.Error("storage", "err", err)
		os.Exit(1)
	}

	st := store.New(pool)
	h := httpapi.New(
		cfg,
		st,
		auth.NewJWT(cfg.JWTSecret, cfg.AccessTokenTTL),
		mailer.Log{Log: logger},
		uploads,
	)

	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go h.RunLivePoll(runCtx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	var tlsSrv *http.Server
	if cfg.MediaEdgeTLSAddr != "" {
		if err := edgetls.Ensure(cfg.MediaEdgeTLSCert, cfg.MediaEdgeTLSKey, cfg.MediaEdgeTLSHost); err != nil {
			logger.Error("media-edge tls cert", "err", err)
			os.Exit(1)
		}
		tlsSrv = &http.Server{
			Addr:              cfg.MediaEdgeTLSAddr,
			Handler:           h,
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			logger.Info("listening-tls", "addr", cfg.MediaEdgeTLSAddr, "host", cfg.MediaEdgeTLSHost)
			if err := tlsSrv.ListenAndServeTLS(cfg.MediaEdgeTLSCert, cfg.MediaEdgeTLSKey); err != nil && err != http.ErrServerClosed {
				logger.Error("https", "err", err)
				os.Exit(1)
			}
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	if tlsSrv != nil {
		_ = tlsSrv.Shutdown(shutdownCtx)
	}
}
