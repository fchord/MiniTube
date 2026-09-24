package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPhase5Live(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "luser" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token(email)}).StatusCode != 204 {
		t.Fatal("verify")
	}
	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")

	created := do(t, h.ts, http.MethodPost, "/v1/live", alice.str("accessToken"), map[string]any{
		"channelId":   chID,
		"title":       "测试直播",
		"description": "hello",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create live: %d %s", created.StatusCode, created.body)
	}
	liveID := created.str("id")
	if created.str("ingestKey") == "" || created.str("ingestUrl") == "" {
		t.Fatalf("missing ingest: %s", created.body)
	}
	if !strings.Contains(created.str("ingestUrl"), "/live/") {
		t.Fatalf("ingest should use SRS app live: %s", created.body)
	}
	if created.str("status") != "scheduled" {
		t.Fatalf("status: %s", created.body)
	}

	got := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, "", nil)
	if got.StatusCode != 200 || got.str("ingestKey") != "" {
		t.Fatalf("public get leaked key: %d %s", got.StatusCode, got.body)
	}

	chLives := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/live", "", nil)
	if chLives.StatusCode != 200 || len(chLives.slice("items")) != 1 {
		t.Fatalf("channel live: %d %s", chLives.StatusCode, chLives.body)
	}
	if _, ok := chLives.slice("items")[0].(map[string]any)["thumbnailUrl"]; !ok {
		t.Fatalf("channel live missing thumbnailUrl: %s", chLives.body)
	}
	pub := do(t, h.ts, http.MethodGet, "/v1/live", "", nil)
	if pub.StatusCode != 200 || len(pub.slice("items")) != 0 {
		t.Fatalf("public list should hide scheduled: %d %s", pub.StatusCode, pub.body)
	}

	id, err := uuid.Parse(liveID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.MarkLiveStarted(context.Background(), id, h.ts.URL+"/v1/live/"+liveID+"/hls/index.m3u8"); err != nil {
		t.Fatal(err)
	}
	liveNow := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, "", nil)
	if liveNow.str("status") != "live" {
		t.Fatalf("started: %s", liveNow.body)
	}
	pub2 := do(t, h.ts, http.MethodGet, "/v1/live", "", nil)
	if pub2.StatusCode != 200 || len(pub2.slice("items")) < 1 {
		t.Fatalf("public live list: %d %s", pub2.StatusCode, pub2.body)
	}
	pb := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/playback", "", nil)
	if pb.StatusCode != 200 || pb.str("masterPlaylistUrl") == "" {
		t.Fatalf("playback: %d %s", pb.StatusCode, pb.body)
	}

	if do(t, h.ts, http.MethodPost, "/v1/live/"+liveID+"/like", alice.str("accessToken"), nil).StatusCode != 204 {
		t.Fatal("like")
	}
	cmt := do(t, h.ts, http.MethodPost, "/v1/live/"+liveID+"/comments", alice.str("accessToken"), map[string]string{"body": "来了"})
	if cmt.StatusCode != 201 {
		t.Fatalf("chat: %d %s", cmt.StatusCode, cmt.body)
	}
	hist := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/comments", "", nil)
	if hist.StatusCode != 200 || len(hist.slice("items")) != 1 {
		t.Fatalf("comments: %d %s", hist.StatusCode, hist.body)
	}

	ended := do(t, h.ts, http.MethodPost, "/v1/live/"+liveID+"/end", alice.str("accessToken"), nil)
	if ended.StatusCode != 200 || ended.str("status") != "ended" {
		t.Fatalf("end: %d %s", ended.StatusCode, ended.body)
	}
	if do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/playback", "", nil).StatusCode != http.StatusConflict {
		t.Fatal("playback after end without recording should 409")
	}
	page := do(t, h.ts, http.MethodGet, "/live/"+liveID, "", nil)
	if page.StatusCode != 200 || !strings.Contains(page.body, "mtPlayer.mount") || !strings.Contains(page.body, "onLiveEnded") {
		t.Fatalf("live page: %d", page.StatusCode)
	}
	dir := do(t, h.ts, http.MethodGet, "/live", "", nil)
	if dir.StatusCode != 200 {
		t.Fatalf("lives page: %d", dir.StatusCode)
	}
}

func TestPhase5SRSHLS(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "suser" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token(email)}).StatusCode != 204 {
		t.Fatal("verify")
	}
	chID := alice.str("user", "defaultChannel", "id")
	created := do(t, h.ts, http.MethodPost, "/v1/live", alice.str("accessToken"), map[string]any{
		"channelId": chID,
		"title":     "SRS 归档",
	})
	if created.StatusCode != 201 {
		t.Fatalf("create: %d %s", created.StatusCode, created.body)
	}
	liveID := created.str("id")
	key := created.str("ingestKey")
	if key == "" {
		t.Fatal("missing key")
	}

	denied := do(t, h.ts, http.MethodPost, "/v1/internal/srs/on_publish", "", map[string]any{
		"action": "on_publish", "app": "live", "stream": key,
	})
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("hook without secret: %d %s", denied.StatusCode, denied.body)
	}

	pub := do(t, h.ts, http.MethodPost, "/v1/internal/srs/on_publish?secret=dev-srs-hook", "", map[string]any{
		"action": "on_publish", "app": "live", "stream": key,
	})
	if pub.StatusCode != 200 || pub.int("code") != 0 {
		t.Fatalf("on_publish: %d %s", pub.StatusCode, pub.body)
	}
	liveNow := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, "", nil)
	if liveNow.str("status") != "live" {
		t.Fatalf("started via hook: %s", liveNow.body)
	}
	if liveNow.str("sizeLabel") != "" {
		t.Fatalf("live should not show size: %s", liveNow.body)
	}

	srsDir := os.Getenv("SRS_HLS_DIR")
	if err := os.MkdirAll(filepath.Join(srsDir, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srsDir, "live", key+"-0.ts"), []byte("tsdata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srsDir, "live", key+".m3u8"), []byte("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2.0,\n"+key+"-0.ts\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	copied := do(t, h.ts, http.MethodPost, "/v1/internal/srs/on_hls?secret=dev-srs-hook", "", map[string]any{
		"action": "on_hls",
		"app":    "live",
		"stream": key,
		"file":   "/data/srs-hls/live/" + key + "-0.ts",
		"m3u8":   "/data/srs-hls/live/" + key + ".m3u8",
	})
	if copied.StatusCode != 200 || copied.int("code") != 0 {
		t.Fatalf("on_hls: %d %s", copied.StatusCode, copied.body)
	}

	pl := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/hls/index.m3u8", "", nil)
	if pl.StatusCode != 200 || !strings.Contains(pl.body, "#EXT-X-PLAYLIST-TYPE:EVENT") || strings.Contains(pl.body, "#EXT-X-ENDLIST") {
		t.Fatalf("live m3u8: %d %s", pl.StatusCode, pl.body)
	}
	seg := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/hls/"+key+"-0.ts", "", nil)
	if seg.StatusCode != 200 || seg.body != "tsdata" {
		t.Fatalf("ts: %d %s", seg.StatusCode, seg.body)
	}

	un := do(t, h.ts, http.MethodPost, "/v1/internal/srs/on_unpublish?secret=dev-srs-hook", "", map[string]any{
		"action": "on_unpublish", "app": "live", "stream": key,
	})
	if un.StatusCode != 200 || un.int("code") != 0 {
		t.Fatalf("on_unpublish: %d %s", un.StatusCode, un.body)
	}
	ended := do(t, h.ts, http.MethodPost, "/v1/live/"+liveID+"/end", alice.str("accessToken"), nil)
	if ended.StatusCode != 200 || ended.str("status") != "ended" {
		t.Fatalf("end: %d %s", ended.StatusCode, ended.body)
	}
	pb := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/playback", "", nil)
	if pb.StatusCode != 200 || pb.str("masterPlaylistUrl") == "" {
		t.Fatalf("playback after end with archive: %d %s", pb.StatusCode, pb.body)
	}
	done := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID+"/hls/index.m3u8", "", nil)
	if done.StatusCode != 200 || !strings.Contains(done.body, "#EXT-X-ENDLIST") {
		t.Fatalf("vod m3u8: %d %s", done.StatusCode, done.body)
	}
	rec := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, alice.str("accessToken"), nil)
	if rec.StatusCode != 200 || rec.str("status") != "ended" || rec.str("sizeLabel") == "" {
		t.Fatalf("ended size: %d %s", rec.StatusCode, rec.body)
	}
}
