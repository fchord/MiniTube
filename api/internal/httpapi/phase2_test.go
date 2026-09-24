package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/ident"
	"minitube/api/internal/transcode"
)

func TestPhase2VOD(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("TRANSCODE_PRESET", "ultrafast")
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "vuser" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	tok := h.mail.Token(email)
	vr := do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": tok})
	if vr.StatusCode != http.StatusNoContent {
		t.Fatalf("verify: %d %s", vr.StatusCode, vr.body)
	}

	chID := alice.str("user", "defaultChannel", "id")
	clip := makeTestClip(t)
	raw, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	created := do(t, h.ts, http.MethodPost, "/v1/videos", alice.str("accessToken"), map[string]any{
		"channelId":  chID,
		"title":      "测试长视频",
		"mimeType":   "video/mp4",
		"filename":   "clip.mp4",
		"size":       len(raw),
		"visibility": "public",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", created.StatusCode, created.body)
	}
	if created.int("upload", "chunkSize") != 8*1024*1024 {
		t.Fatalf("chunk size: %s", created.body)
	}
	if created.int("video", "sizeBytes") != len(raw) {
		t.Fatalf("sizeBytes: %s", created.body)
	}
	videoID := created.str("video", "id")
	if !ident.ValidVideoID(videoID) {
		t.Fatalf("video id %q", videoID)
	}
	putVideoChunks(t, h.ts.URL, alice.str("accessToken"), videoID, raw)

	done := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/complete-upload", alice.str("accessToken"), nil)
	if done.StatusCode != http.StatusAccepted {
		t.Fatalf("complete: %d %s", done.StatusCode, done.body)
	}

	v, err := h.store.GetVideo(context.Background(), videoID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := transcode.Process(ctx, h.store, h.uploads, v); err != nil {
		t.Fatalf("transcode: %v", err)
	}

	got := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, "", nil)
	if got.StatusCode != 200 || got.str("status") != "ready" {
		t.Fatalf("ready video: %d %s", got.StatusCode, got.body)
	}

	pb := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID+"/playback", "", nil)
	if pb.StatusCode != 200 || pb.str("masterPlaylistUrl") == "" {
		t.Fatalf("playback: %d %s", pb.StatusCode, pb.body)
	}
	master := pb.str("masterPlaylistUrl")
	if i := strings.Index(master, "/v1/media/"); i >= 0 {
		master = master[i:]
	}
	mres, err := http.Get(h.ts.URL + master)
	if err != nil {
		t.Fatal(err)
	}
	defer mres.Body.Close()
	if mres.StatusCode != 200 {
		t.Fatalf("master m3u8: %d", mres.StatusCode)
	}

	handle := alice.str("user", "defaultChannel", "handle")
	list := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/videos", "", nil)
	if list.StatusCode != 200 || len(list.slice("items")) != 1 {
		t.Fatalf("channel videos: %d %s", list.StatusCode, list.body)
	}

	like := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/like", alice.str("accessToken"), nil)
	if like.StatusCode != http.StatusNoContent {
		t.Fatalf("like: %d %s", like.StatusCode, like.body)
	}
	cmt := do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/comments", alice.str("accessToken"), map[string]string{"body": "好看"})
	if cmt.StatusCode != http.StatusCreated {
		t.Fatalf("comment: %d %s", cmt.StatusCode, cmt.body)
	}
	prog := do(t, h.ts, http.MethodPut, "/v1/videos/"+videoID+"/progress", alice.str("accessToken"), map[string]any{"positionMs": 500, "completed": false})
	if prog.StatusCode != http.StatusNoContent {
		t.Fatalf("progress: %d %s", prog.StatusCode, prog.body)
	}
	hist := do(t, h.ts, http.MethodGet, "/v1/me/watch-history", alice.str("accessToken"), nil)
	if hist.StatusCode != 200 || len(hist.slice("items")) != 1 {
		t.Fatalf("history: %d %s", hist.StatusCode, hist.body)
	}

	favs := do(t, h.ts, http.MethodGet, "/v1/me/favorites", alice.str("accessToken"), nil)
	listID := ""
	for _, it := range favs.slice("items") {
		m, _ := it.(map[string]any)
		if m["kind"] == "watch_later" {
			listID, _ = m["id"].(string)
		}
	}
	if listID == "" {
		t.Fatalf("no watch_later: %s", favs.body)
	}
	add := do(t, h.ts, http.MethodPost, "/v1/favorites/"+listID+"/items", alice.str("accessToken"), map[string]string{
		"targetType": "video", "targetId": videoID,
	})
	if add.StatusCode != http.StatusNoContent {
		t.Fatalf("favorite: %d %s", add.StatusCode, add.body)
	}

	page := do(t, h.ts, http.MethodGet, "/watch/"+videoID, "", nil)
	if page.StatusCode != 200 || !strings.Contains(page.body, "hls.js") || !strings.Contains(page.body, "player.js") {
		t.Fatalf("watch page: %d", page.StatusCode)
	}
	pub := do(t, h.ts, http.MethodGet, "/publish?channel="+chID+"&kind=long", "", nil)
	if pub.StatusCode != 200 || !strings.Contains(pub.body, "选择文件") {
		t.Fatalf("publish page: %d", pub.StatusCode)
	}
}

func putVideoChunks(t *testing.T, tsURL, token, videoID string, raw []byte) {
	t.Helper()
	const cs = 8 * 1024 * 1024
	n := (len(raw) + cs - 1) / cs
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		a := i * cs
		b := a + cs
		if b > len(raw) {
			b = len(raw)
		}
		req, err := http.NewRequest(http.MethodPut, tsURL+"/v1/videos/"+videoID+"/chunks/"+strconv.Itoa(i), bytes.NewReader(raw[a:b]))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
			t.Fatalf("chunk %d: %d", i, res.StatusCode)
		}
	}
}

func makeTestClip(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=1:size=640x360:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=880:duration=1",
		"-shortest", "-pix_fmt", "yuv420p",
		"-c:v", "libx264", "-preset", "ultrafast",
		"-c:a", "aac",
		out,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffmpeg lavfi: %v", err)
	}
	return out
}

func TestVideoPrivate(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("TRANSCODE_PRESET", "ultrafast")
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "pva"+suffix, "pva"+suffix+"@example.com", "password12")
	bob := mustRegister(t, h, "pvb"+suffix, "pvb"+suffix+"@example.com", "password12")
	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")
	clip := makeTestClip(t)
	raw, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	created := do(t, h.ts, http.MethodPost, "/v1/videos", alice.str("accessToken"), map[string]any{
		"channelId":  chID,
		"title":      "私藏片",
		"mimeType":   "video/mp4",
		"filename":   "clip.mp4",
		"size":       len(raw),
		"visibility": "private",
	})
	if created.StatusCode != http.StatusCreated || created.str("video", "visibility") != "private" {
		t.Fatalf("create private: %d %s", created.StatusCode, created.body)
	}
	videoID := created.str("video", "id")
	putVideoChunks(t, h.ts.URL, alice.str("accessToken"), videoID, raw)
	if do(t, h.ts, http.MethodPost, "/v1/videos/"+videoID+"/complete-upload", alice.str("accessToken"), nil).StatusCode != http.StatusAccepted {
		t.Fatal("complete")
	}
	v, err := h.store.GetVideo(context.Background(), videoID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := transcode.Process(ctx, h.store, h.uploads, v); err != nil {
		t.Fatalf("transcode: %v", err)
	}
	anon := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, "", nil)
	if anon.StatusCode != http.StatusForbidden || anon.str("code") != "private" {
		t.Fatalf("anon get: %d %s", anon.StatusCode, anon.body)
	}
	if !strings.Contains(anon.body, "私藏") {
		t.Fatalf("anon message: %s", anon.body)
	}
	other := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, bob.str("accessToken"), nil)
	if other.StatusCode != http.StatusForbidden {
		t.Fatalf("bob get: %d %s", other.StatusCode, other.body)
	}
	if do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID+"/playback", "", nil).StatusCode != http.StatusForbidden {
		t.Fatal("anon playback")
	}
	own := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID, alice.str("accessToken"), nil)
	if own.StatusCode != 200 || own.str("visibility") != "private" {
		t.Fatalf("owner get: %d %s", own.StatusCode, own.body)
	}
	if do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID+"/playback", alice.str("accessToken"), nil).StatusCode != 200 {
		t.Fatal("owner playback")
	}
	pubList := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/videos", "", nil)
	if pubList.StatusCode != 200 || len(pubList.slice("items")) != 0 {
		t.Fatalf("public list: %d %s", pubList.StatusCode, pubList.body)
	}
	ownList := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/videos", alice.str("accessToken"), nil)
	if ownList.StatusCode != 200 || len(ownList.slice("items")) != 1 {
		t.Fatalf("owner list: %d %s", ownList.StatusCode, ownList.body)
	}
}
