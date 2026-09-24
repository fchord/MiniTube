package httpapi_test

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/transcode"
)

func TestPhase4Shorts(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "suser" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token(email)}).StatusCode != 204 {
		t.Fatal("verify")
	}
	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")

	created := do(t, h.ts, http.MethodPost, "/v1/videos", alice.str("accessToken"), map[string]any{
		"channelId": chID,
		"title":     "一条短视频",
		"kind":      "short",
		"mimeType":  "video/mp4",
		"filename":  "short.mp4",
		"size":      1,
	})
	if created.StatusCode != http.StatusCreated || created.str("video", "kind") != "short" {
		t.Fatalf("create short: %d %s", created.StatusCode, created.body)
	}
	videoID := created.str("video", "id")

	empty := do(t, h.ts, http.MethodGet, "/v1/feed/shorts", "", nil)
	if empty.StatusCode != 200 {
		t.Fatalf("feed before ready: %d %s", empty.StatusCode, empty.body)
	}
	for _, it := range empty.slice("items") {
		m, _ := it.(map[string]any)
		if m["id"] == videoID {
			t.Fatal("processing short should not appear in feed")
		}
	}

	page := do(t, h.ts, http.MethodGet, "/shorts", "", nil)
	if page.StatusCode != 200 || !strings.Contains(page.body, "/v1/feed/shorts") || !strings.Contains(page.body, "random=1") {
		t.Fatalf("shorts page: %d", page.StatusCode)
	}
	chPage := do(t, h.ts, http.MethodGet, "/c/"+handle, "", nil)
	if chPage.StatusCode != 200 || !strings.Contains(chPage.body, `query: "?ch="`) {
		t.Fatalf("channel shorts should link with ch=: %d", chPage.StatusCode)
	}
	one := do(t, h.ts, http.MethodGet, "/shorts/"+videoID, "", nil)
	if one.StatusCode != 200 {
		t.Fatalf("shorts item page: %d", one.StatusCode)
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("TRANSCODE_PRESET", "ultrafast")

	clip := makeTestClip(t)
	raw, err := os.ReadFile(clip)
	if err != nil {
		t.Fatal(err)
	}
	created2 := do(t, h.ts, http.MethodPost, "/v1/videos", alice.str("accessToken"), map[string]any{
		"channelId": chID,
		"title":     "一条短视频",
		"kind":      "short",
		"mimeType":  "video/mp4",
		"filename":  "short.mp4",
		"size":      len(raw),
	})
	if created2.StatusCode != http.StatusCreated {
		t.Fatalf("create short file: %d %s", created2.StatusCode, created2.body)
	}
	videoID = created2.str("video", "id")
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
	if got.StatusCode != 200 || got.str("status") != "ready" || got.str("kind") != "short" {
		t.Fatalf("ready short: %d %s", got.StatusCode, got.body)
	}
	longs := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/videos", "", nil)
	if longs.StatusCode != 200 || len(longs.slice("items")) != 0 {
		t.Fatalf("long list should be empty: %d %s", longs.StatusCode, longs.body)
	}
	shorts := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/videos?kind=short", "", nil)
	if shorts.StatusCode != 200 || len(shorts.slice("items")) != 1 {
		t.Fatalf("channel shorts: %d %s", shorts.StatusCode, shorts.body)
	}
	feed := do(t, h.ts, http.MethodGet, "/v1/feed/shorts", "", nil)
	if feed.StatusCode != 200 || len(feed.slice("items")) < 1 {
		t.Fatalf("feed: %d %s", feed.StatusCode, feed.body)
	}
	rnd := do(t, h.ts, http.MethodGet, "/v1/feed/shorts?random=1&exclude="+videoID, "", nil)
	if rnd.StatusCode != 200 {
		t.Fatalf("random feed: %d %s", rnd.StatusCode, rnd.body)
	}
	for _, it := range rnd.slice("items") {
		m, _ := it.(map[string]any)
		if m["id"] == videoID {
			t.Fatal("excluded short still in random feed")
		}
	}
	found := false
	for _, it := range feed.slice("items") {
		m, _ := it.(map[string]any)
		if m["id"] == videoID {
			found = true
			if m["kind"] != "short" {
				t.Fatalf("feed item kind: %v", m["kind"])
			}
		}
	}
	if !found {
		t.Fatalf("short missing from feed: %s", feed.body)
	}
	pb := do(t, h.ts, http.MethodGet, "/v1/videos/"+videoID+"/playback", "", nil)
	if pb.StatusCode != 200 || pb.str("masterPlaylistUrl") == "" {
		t.Fatalf("playback: %d %s", pb.StatusCode, pb.body)
	}
}
