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

func TestChannelManageTrash(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "mga"+suffix, "mga"+suffix+"@example.com", "password12")
	bob := mustRegister(t, h, "mgb"+suffix, "mgb"+suffix+"@example.com", "password12")
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")

	page := do(t, h.ts, http.MethodGet, "/c/"+handle+"/manage", "", nil)
	if page.StatusCode != 200 || !strings.Contains(page.body, "const MANAGE = true") || !strings.Contains(page.body, "回收站") {
		t.Fatalf("manage page: %d", page.StatusCode)
	}
	chPage := do(t, h.ts, http.MethodGet, "/c/"+handle, "", nil)
	if chPage.StatusCode != 200 || !strings.Contains(chPage.body, "const MANAGE = false") {
		t.Fatalf("channel page: %d", chPage.StatusCode)
	}

	created := do(t, h.ts, http.MethodPost, "/v1/videos", tok, map[string]any{
		"channelId": chID,
		"title":     "管理片",
		"mimeType":  "video/mp4",
		"filename":  "clip.mp4",
		"size":      12,
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create video: %d %s", created.StatusCode, created.body)
	}
	videoID := created.str("video", "id")

	hidden := do(t, h.ts, http.MethodPatch, "/v1/videos/"+videoID, tok, map[string]string{"visibility": "private"})
	if hidden.StatusCode != 200 || hidden.str("visibility") != "private" {
		t.Fatalf("hide: %d %s", hidden.StatusCode, hidden.body)
	}

	del := do(t, h.ts, http.MethodDelete, "/v1/videos/"+videoID, tok, nil)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("recycle: %d %s", del.StatusCode, del.body)
	}
	if do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, tok, nil).StatusCode != http.StatusNotFound {
		t.Fatal("recycled video should 404")
	}

	anonTrash := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/trash", "", nil)
	if anonTrash.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon trash: %d %s", anonTrash.StatusCode, anonTrash.body)
	}
	bobTrash := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/trash", bob.str("accessToken"), nil)
	if bobTrash.StatusCode != http.StatusForbidden {
		t.Fatalf("bob trash: %d %s", bobTrash.StatusCode, bobTrash.body)
	}
	trash := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/trash", tok, nil)
	if trash.StatusCode != 200 || len(trash.slice("videos")) != 1 {
		t.Fatalf("owner trash: %d %s", trash.StatusCode, trash.body)
	}

	restored := do(t, h.ts, http.MethodPost, "/v1/channels/"+handle+"/manage", tok, map[string]any{
		"action":   "restore",
		"videoIds": []string{videoID},
	})
	if restored.StatusCode != 200 {
		t.Fatalf("restore: %d %s", restored.StatusCode, restored.body)
	}
	own := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, tok, nil)
	if own.StatusCode != 200 || own.str("visibility") != "public" {
		t.Fatalf("restored: %d %s", own.StatusCode, own.body)
	}

	if do(t, h.ts, http.MethodDelete, "/v1/videos/"+videoID, tok, nil).StatusCode != http.StatusNoContent {
		t.Fatal("recycle again")
	}
	purged := do(t, h.ts, http.MethodDelete, "/v1/videos/"+videoID+"?purge=1", tok, nil)
	if purged.StatusCode != http.StatusNoContent {
		t.Fatalf("purge: %d %s", purged.StatusCode, purged.body)
	}
	empty := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/trash", tok, nil)
	if empty.StatusCode != 200 || len(empty.slice("videos")) != 0 {
		t.Fatalf("trash after purge: %s", empty.body)
	}
}

func TestPrivateHLSMediaForbidden(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "mha"+suffix, "mha"+suffix+"@example.com", "password12")
	bob := mustRegister(t, h, "mhb"+suffix, "mhb"+suffix+"@example.com", "password12")
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	created := do(t, h.ts, http.MethodPost, "/v1/videos", tok, map[string]any{
		"channelId": chID,
		"title":     "隐藏片源",
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
	if _, err := h.store.SetVideoVisibility(context.Background(), videoID, "private"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.uploads.Dir(), "hls", videoID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "master.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := "/v1/media/hls/" + videoID + "/master.m3u8"
	anon := do(t, h.ts, http.MethodGet, path, "", nil)
	if anon.StatusCode != http.StatusForbidden {
		t.Fatalf("anon media: %d %s", anon.StatusCode, anon.body)
	}
	other := do(t, h.ts, http.MethodGet, path, bob.str("accessToken"), nil)
	if other.StatusCode != http.StatusForbidden {
		t.Fatalf("bob media: %d %s", other.StatusCode, other.body)
	}
	own := do(t, h.ts, http.MethodGet, path, tok, nil)
	if own.StatusCode != 200 || !strings.Contains(own.body, "#EXTM3U") {
		t.Fatalf("owner media: %d %s", own.StatusCode, own.body)
	}
}

func TestDeletedHLSMediaOwnerCanReadThumb(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "mda"+suffix, "mda"+suffix+"@example.com", "password12")
	bob := mustRegister(t, h, "mdb"+suffix, "mdb"+suffix+"@example.com", "password12")
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	created := do(t, h.ts, http.MethodPost, "/v1/videos", tok, map[string]any{
		"channelId": chID,
		"title":     "回收封面",
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
		ThumbnailKey: "http://example.invalid/v1/media/hls/" + videoID + "/thumb.jpg",
	}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.uploads.Dir(), "hls", videoID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "thumb.jpg"), []byte("jpeg-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if do(t, h.ts, http.MethodDelete, "/v1/videos/"+videoID, tok, nil).StatusCode != http.StatusNoContent {
		t.Fatal("recycle")
	}
	if do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, tok, nil).StatusCode != http.StatusNotFound {
		t.Fatal("recycled metadata should 404")
	}
	path := "/v1/media/hls/" + videoID + "/thumb.jpg"
	anon := do(t, h.ts, http.MethodGet, path, "", nil)
	if anon.StatusCode != http.StatusNotFound {
		t.Fatalf("anon deleted media: %d %s", anon.StatusCode, anon.body)
	}
	other := do(t, h.ts, http.MethodGet, path, bob.str("accessToken"), nil)
	if other.StatusCode != http.StatusNotFound {
		t.Fatalf("bob deleted media: %d %s", other.StatusCode, other.body)
	}
	own := do(t, h.ts, http.MethodGet, path, tok, nil)
	if own.StatusCode != 200 || own.body != "jpeg-bytes" {
		t.Fatalf("owner deleted thumb: %d %s", own.StatusCode, own.body)
	}
}

func TestChannelManageLive(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "mla"+suffix, "mla"+suffix+"@example.com", "password12")
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token("mla" + suffix + "@example.com")}).StatusCode != 204 {
		t.Fatal("verify")
	}
	tok := alice.str("accessToken")
	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")

	created := do(t, h.ts, http.MethodPost, "/v1/live", tok, map[string]any{
		"channelId": chID,
		"title":     "管理直播",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create live: %d %s", created.StatusCode, created.body)
	}
	liveID := created.str("id")
	if created.str("visibility") != "public" {
		t.Fatalf("visibility: %s", created.body)
	}

	hid := do(t, h.ts, http.MethodPatch, "/v1/live/"+liveID, tok, map[string]string{"visibility": "private"})
	if hid.StatusCode != 200 || hid.str("visibility") != "private" {
		t.Fatalf("hide live: %d %s", hid.StatusCode, hid.body)
	}
	if do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, "", nil).StatusCode != http.StatusForbidden {
		t.Fatal("anon private live")
	}
	chLives := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/live", "", nil)
	if chLives.StatusCode != 200 || len(chLives.slice("items")) != 0 {
		t.Fatalf("public channel live should hide private: %s", chLives.body)
	}

	id, err := uuid.Parse(liveID)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.MarkLiveStarted(context.Background(), id, h.ts.URL+"/v1/live/"+liveID+"/hls/index.m3u8"); err != nil {
		t.Fatal(err)
	}
	busy := do(t, h.ts, http.MethodDelete, "/v1/live/"+liveID, tok, nil)
	if busy.StatusCode != http.StatusConflict {
		t.Fatalf("live in progress should 409: %d %s", busy.StatusCode, busy.body)
	}
	batch := do(t, h.ts, http.MethodPost, "/v1/channels/"+handle+"/manage", tok, map[string]any{
		"action":  "recycle",
		"liveIds": []string{liveID},
	})
	if batch.StatusCode != 200 {
		t.Fatalf("batch skip live: %d %s", batch.StatusCode, batch.body)
	}
	still := do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, tok, nil)
	if still.StatusCode != 200 || still.str("status") != "live" || still.str("visibility") != "private" {
		t.Fatalf("live should be skipped: %s", still.body)
	}

	ended := do(t, h.ts, http.MethodPost, "/v1/live/"+liveID+"/end", tok, nil)
	if ended.StatusCode != 200 {
		t.Fatalf("end: %d %s", ended.StatusCode, ended.body)
	}
	if do(t, h.ts, http.MethodDelete, "/v1/live/"+liveID, tok, nil).StatusCode != http.StatusNoContent {
		t.Fatal("recycle ended live")
	}
	trash := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/trash", tok, nil)
	if trash.StatusCode != 200 || len(trash.slice("lives")) != 1 {
		t.Fatalf("live trash: %s", trash.body)
	}
	key := created.str("ingestKey")
	if key != "" {
		dir := os.Getenv("LIVE_HLS_DIR")
		_ = os.MkdirAll(filepath.Join(dir, key), 0o755)
		_ = os.WriteFile(filepath.Join(dir, key, "index.m3u8"), []byte("#EXTM3U\n"), 0o644)
	}
	if do(t, h.ts, http.MethodDelete, "/v1/live/"+liveID+"?purge=1", tok, nil).StatusCode != http.StatusNoContent {
		t.Fatal("purge live")
	}
	if do(t, h.ts, http.MethodGet, "/v1/live/"+liveID, tok, nil).StatusCode != http.StatusNotFound {
		t.Fatal("purged live should 404")
	}
	if key != "" {
		if _, err := os.Stat(filepath.Join(os.Getenv("LIVE_HLS_DIR"), key)); !os.IsNotExist(err) {
			t.Fatalf("archive should be removed: %v", err)
		}
	}
}
