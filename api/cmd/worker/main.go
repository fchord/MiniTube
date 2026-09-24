package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"minitube/api/internal/config"
	"minitube/api/internal/db"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
	"minitube/api/internal/transcode"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	cfg := config.FromEnv()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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

	w := workerFromEnv()
	logger.Info("transcode worker started", "uploadDir", cfg.UploadDir, "id", w.ID, "rank", w.Rank, "encoder", w.Encoder)
	transcode.RunLoop(ctx, store.New(pool), uploads, 2*time.Second, w)
}

func workerFromEnv() *transcode.Worker {
	rank, _ := strconv.Atoi(os.Getenv("TRANSCODE_RANK"))
	id := os.Getenv("TRANSCODE_WORKER_ID")
	if id == "" {
		id, _ = os.Hostname()
	}
	enc := os.Getenv("TRANSCODE_ENCODER")
	if enc == "" {
		enc = "libx264"
	}
	return &transcode.Worker{ID: id, Rank: rank, Encoder: enc}
}
