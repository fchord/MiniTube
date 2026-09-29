package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"minitube/api/internal/store"
)

func TestPlaybackClientReport(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "cap"+suffix, "cap"+suffix+"@example.com", "password12")
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	created := do(t, h.ts, http.MethodPost, "/v1/videos", tok, map[string]any{
		"channelId": chID,
		"title":     "能力探测",
		"mimeType":  "video/mp4",
		"filename":  "clip.mp4",
		"size":      12,
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", created.StatusCode, created.body)
	}
	videoID := created.str("video", "id")
	if err := h.store.SaveTranscodeSuccess(context.Background(), videoID, store.TranscodeResult{
		DurationMs: 1000, Width: 64, Height: 36, AspectRatio: 16.0 / 9,
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.uploads.Dir(), "hls", videoID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/playback", tok, map[string]any{
		"client": map[string]any{
			"page":           "watch",
			"os":             "windows",
			"osVersion":      "15.0.0",
			"browser":        "chrome",
			"browserVersion": "130.0.0.0",
			"hwCodecs":       []string{"h264", "av1"},
			"ua":             "Mozilla/5.0 test",
			"codecs": []any{
				map[string]any{
					"id": "h264", "hardware": true,
					"max": map[string]any{"width": 3840, "height": 2160, "bitrate": 8e7, "profile": "High", "level": "5.1"},
				},
			},
		},
	})
	if got.StatusCode != 200 || got.str("masterPlaylistUrl") == "" {
		t.Fatalf("playback post: %d %s", got.StatusCode, got.body)
	}
	if got.str("client", "os") != "windows" || got.str("client", "browser") != "chrome" {
		t.Fatalf("echo client: %s", got.body)
	}

	var osName, browser string
	var hw []string
	err := h.store.Pool().QueryRow(context.Background(), `
		SELECT os, browser, hw_codecs FROM playback_client_reports WHERE video_id = $1
		ORDER BY created_at DESC LIMIT 1`, videoID).Scan(&osName, &browser, &hw)
	if err != nil {
		t.Fatal(err)
	}
	if osName != "windows" || browser != "chrome" || len(hw) != 2 {
		t.Fatalf("stored %s %s %v", osName, browser, hw)
	}

	get := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID+"/playback", "", nil)
	if get.StatusCode != 200 {
		t.Fatalf("get playback: %d %s", get.StatusCode, get.body)
	}
}

func TestPlaybackPicksHEVCWhenClientHasHW(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "hevc"+suffix, "hevc"+suffix+"@example.com", "password12")
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	created := do(t, h.ts, http.MethodPost, "/v1/videos", tok, map[string]any{
		"channelId": chID, "title": "双编码", "mimeType": "video/mp4", "filename": "clip.mp4", "size": 12,
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", created.StatusCode, created.body)
	}
	videoID := created.str("video", "id")
	if err := h.store.SaveTranscodeSuccess(context.Background(), videoID, store.TranscodeResult{
		DurationMs: 1000, Width: 1280, Height: 720, AspectRatio: 16.0 / 9,
		Renditions: []store.Rendition{
			{Height: 720, BandwidthBps: 1128000, PlaylistKey: "hls/" + videoID + "/avc/720/index.m3u8", Codec: "avc1"},
			{Height: 720, BandwidthBps: 828000, PlaylistKey: "hls/" + videoID + "/hevc/720/index.m3u8", Codec: "hvc1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"avc", "hevc"} {
		dir := filepath.Join(h.uploads.Dir(), "hls", videoID, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.uploads.Dir(), "hls", videoID, "master.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	avc := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/playback", tok, map[string]any{
		"client": map[string]any{"hwCodecs": []string{"h264", "vp9"}, "page": "watch"},
	})
	if avc.StatusCode != 200 || avc.str("codec") != "avc" || !strings.Contains(avc.str("masterPlaylistUrl"), "/avc/") {
		t.Fatalf("avc pick: %d %s", avc.StatusCode, avc.body)
	}
	if len(avc.slice("renditions")) != 1 || avc.slice("renditions")[0].(map[string]any)["codec"] != "avc1" {
		t.Fatalf("avc renditions: %s", avc.body)
	}

	hevc := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/playback", tok, map[string]any{
		"client": map[string]any{"hwCodecs": []string{"h264", "hevc"}, "page": "shorts"},
	})
	if hevc.StatusCode != 200 || hevc.str("codec") != "hevc" || !strings.Contains(hevc.str("masterPlaylistUrl"), "/hevc/") {
		t.Fatalf("hevc pick: %d %s", hevc.StatusCode, hevc.body)
	}
	if len(hevc.slice("renditions")) != 1 || hevc.slice("renditions")[0].(map[string]any)["codec"] != "hvc1" {
		t.Fatalf("hevc renditions: %s", hevc.body)
	}
}
