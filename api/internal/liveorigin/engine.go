package liveorigin

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/config"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

var ErrLiveEnded = errors.New("live already ended")

type Engine struct {
	Cfg     config.Config
	Store   *store.Postgres
	Uploads *storage.Local
	OnEnded func(store.Live)
}

func New(cfg config.Config, st *store.Postgres, up *storage.Local) *Engine {
	return &Engine{Cfg: cfg, Store: st, Uploads: up}
}

func (e *Engine) PlaybackURL(id uuid.UUID) string {
	return strings.TrimRight(e.Cfg.PublicBaseURL, "/") + "/v1/live/" + id.String() + "/hls/index.m3u8"
}

func IdleAnchor(unpublished, lastHLS *time.Time) *time.Time {
	if unpublished == nil {
		return nil
	}
	if lastHLS != nil && lastHLS.After(*unpublished) {
		return lastHLS
	}
	return unpublished
}

func ShouldEndIdle(unpublished, lastHLS *time.Time, ttl time.Duration, now time.Time) bool {
	a := IdleAnchor(unpublished, lastHLS)
	if a == nil {
		return false
	}
	return !now.Before(a.Add(ttl))
}

func (e *Engine) notifyEnded(live store.Live) {
	if e.OnEnded == nil {
		return
	}
	e.OnEnded(live)
}

func (e *Engine) recordArchiveSize(ctx context.Context, liveID uuid.UUID, ingestKey string) {
	if e.Store == nil {
		return
	}
	n := e.ArchiveSize(ingestKey)
	if n < 1 {
		return
	}
	_ = e.Store.SetLiveArchiveBytes(ctx, liveID, n)
}

func (e *Engine) OnPublish(ctx context.Context, stream string) error {
	live, err := e.Store.GetLiveByIngestKey(ctx, stream)
	if err != nil {
		return err
	}
	if live.Visibility == "deleted" {
		return store.ErrNotFound
	}
	if live.Status == "ended" {
		return ErrLiveEnded
	}
	return e.Store.MarkLiveStarted(ctx, live.ID, e.PlaybackURL(live.ID))
}

func (e *Engine) OnUnpublish(ctx context.Context, stream string) error {
	live, err := e.Store.GetLiveByIngestKey(ctx, stream)
	if err != nil {
		return err
	}
	if err := e.Store.MarkLiveUnpublished(ctx, live.ID); err != nil {
		return err
	}
	_ = e.EnsureEndList(stream)
	e.maybeThumb(stream)
	go func() {
		time.Sleep(1500 * time.Millisecond)
		if err := e.EnsureEndList(stream); err != nil {
			slog.Warn("endlist", "stream", stream, "err", err)
		}
		e.recordArchiveSize(context.Background(), live.ID, stream)
	}()
	return nil
}

func (e *Engine) End(ctx context.Context, id uuid.UUID) error {
	live, err := e.Store.GetLive(ctx, id)
	if err != nil {
		return err
	}
	if live.IngestKey != "" {
		if err := e.EnsureEndList(live.IngestKey); err != nil {
			slog.Warn("endlist", "id", live.ID, "err", err)
		}
	}
	if live.Status != "ended" {
		if err := e.Store.MarkLiveEnded(ctx, id); err != nil {
			return err
		}
		live, err = e.Store.GetLive(ctx, id)
		if err != nil {
			return err
		}
		e.notifyEnded(live)
	}
	if live.IngestKey != "" {
		e.recordArchiveSize(ctx, live.ID, live.IngestKey)
	}
	return nil
}
