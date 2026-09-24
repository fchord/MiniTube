package liveorigin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"minitube/api/internal/config"
)

func TestRewritePlaylistAddsEventAndEndlist(t *testing.T) {
	in := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\nfoo-0.ts\n")
	live := string(RewritePlaylist(in, false))
	if !strings.Contains(live, "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatalf("live playlist: %s", live)
	}
	if strings.Contains(live, "#EXT-X-ENDLIST") {
		t.Fatalf("live should not end: %s", live)
	}
	done := string(RewritePlaylist([]byte(live), true))
	if !strings.Contains(done, "#EXT-X-PLAYLIST-TYPE:VOD") || !strings.Contains(done, "#EXT-X-ENDLIST") {
		t.Fatalf("complete playlist: %s", done)
	}
}

func TestShouldEndIdle(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	unpub := now.Add(-30 * time.Minute)
	if ShouldEndIdle(&unpub, nil, time.Hour, now) {
		t.Fatal("should wait until 1h after unpublish")
	}
	unpub = now.Add(-2 * time.Hour)
	if !ShouldEndIdle(&unpub, nil, time.Hour, now) {
		t.Fatal("should end after idle")
	}
	last := now.Add(-10 * time.Minute)
	if ShouldEndIdle(&unpub, &last, time.Hour, now) {
		t.Fatal("viewer HLS should keep the room open")
	}
	if ShouldEndIdle(nil, &last, time.Hour, now) {
		t.Fatal("still publishing")
	}
}

func TestEnsureEndList(t *testing.T) {
	dir := t.TempDir()
	e := New(config.Config{LiveHLSDir: dir, SRSHLSDir: dir}, nil, nil)
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.MkdirAll(e.ArchiveDir(key), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(e.ArchiveDir(key), "index.m3u8")
	if err := os.WriteFile(src, []byte("#EXTM3U\n#EXTINF:2.0,\na.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.EnsureEndList(key); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "#EXT-X-ENDLIST") {
		t.Fatalf("missing endlist: %s", body)
	}
}

func TestOnHLSCopiesAndRewrites(t *testing.T) {
	srs := t.TempDir()
	live := t.TempDir()
	e := New(config.Config{LiveHLSDir: live, SRSHLSDir: srs}, nil, nil)
	if err := os.MkdirAll(filepath.Join(srs, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	ts := filepath.Join(srs, "live", "abc-0.ts")
	m3u8 := filepath.Join(srs, "live", "abc.m3u8")
	if err := os.WriteFile(ts, []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m3u8, []byte("#EXTM3U\n#EXTINF:2.0,\nabc-0.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.OnHLS(context.Background(), "abc", "/data/srs-hls/live/abc-0.ts", "/data/srs-hls/live/abc.m3u8"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(live, "abc", "index.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatalf("copied playlist: %s", got)
	}
	if _, err := os.Stat(filepath.Join(live, "abc", "abc-0.ts")); err != nil {
		t.Fatal(err)
	}
}
