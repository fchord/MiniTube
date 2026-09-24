package liveorigin

import (
	"context"
	"log/slog"
	"time"
)

func (e *Engine) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		_ = e.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) Poll(ctx context.Context) error {
	opens, err := e.Store.ListOpenLives(ctx)
	if err != nil {
		return err
	}
	now := time.Now()
	ttl := e.Cfg.LiveIdleTTL
	if ttl < 0 {
		ttl = 0
	}
	for _, live := range opens {
		if live.Status != "live" || live.UnpublishedAt == nil {
			continue
		}
		if live.IngestKey != "" {
			if err := e.EnsureEndList(live.IngestKey); err != nil {
				slog.Warn("endlist", "id", live.ID, "err", err)
			}
		}
		if !ShouldEndIdle(live.UnpublishedAt, live.LastHLSAt, ttl, now) {
			continue
		}
		if err := e.End(ctx, live.ID); err != nil {
			slog.Warn("end live", "id", live.ID, "err", err)
		}
	}
	e.backfillThumbs(ctx)
	return nil
}
