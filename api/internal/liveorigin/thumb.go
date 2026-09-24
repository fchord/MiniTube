package liveorigin

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"minitube/api/internal/store"
	"minitube/api/internal/transcode"
)

var thumbInflight sync.Map
var thumbTried sync.Map

func (e *Engine) maybeThumb(stream string) {
	if e == nil || e.Store == nil || e.Uploads == nil || stream == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		live, err := e.Store.GetLiveByIngestKey(ctx, stream)
		if err != nil {
			return
		}
		e.captureThumb(ctx, live)
	}()
}

func (e *Engine) backfillThumbs(ctx context.Context) {
	if e == nil || e.Store == nil || e.Uploads == nil {
		return
	}
	lives, err := e.Store.ListLivesMissingThumbnail(ctx, 10)
	if err != nil {
		return
	}
	for _, live := range lives {
		if t, ok := thumbTried.Load(live.ID.String()); ok {
			if at, ok := t.(time.Time); ok && time.Since(at) < 30*time.Second {
				continue
			}
		}
		thumbTried.Store(live.ID.String(), time.Now())
		live := live
		go func() {
			c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			e.captureThumb(c, live)
		}()
	}
}

func (e *Engine) captureThumb(ctx context.Context, live store.Live) {
	if live.ThumbnailURL != nil && *live.ThumbnailURL != "" {
		return
	}
	if live.IngestKey == "" {
		return
	}
	if _, loaded := thumbInflight.LoadOrStore(live.ID.String(), true); loaded {
		return
	}
	defer thumbInflight.Delete(live.ID.String())

	src := pickThumbSource(e.ArchiveDir(live.IngestKey))
	if src == "" {
		return
	}
	st, err := os.Stat(src)
	if err != nil || st.Size() < 1024 {
		return
	}
	rel := filepath.ToSlash(filepath.Join("live", live.ID.String(), "thumb.jpg"))
	dst, err := e.Uploads.Abs(rel)
	if err != nil {
		slog.Warn("live thumb path", "id", live.ID, "err", err)
		return
	}
	if err := transcode.ExtractJPEG(ctx, src, dst); err != nil {
		slog.Warn("live thumb ffmpeg", "id", live.ID, "err", err)
		return
	}
	if err := e.Store.SetLiveThumbnail(ctx, live.ID, e.Uploads.URL(rel)); err != nil {
		slog.Warn("live thumb save", "id", live.ID, "err", err)
	}
}

func pickThumbSource(dir string) string {
	matches, err := filepath.Glob(filepath.Join(dir, "*.ts"))
	if err == nil && len(matches) > 0 {
		sort.Strings(matches)
		return matches[(len(matches)-1)*1/10]
	}
	m3u8 := filepath.Join(dir, "index.m3u8")
	if st, err := os.Stat(m3u8); err == nil && !st.IsDir() {
		return m3u8
	}
	return ""
}
