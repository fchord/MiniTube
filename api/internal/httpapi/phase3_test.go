package httpapi_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPhase3Posts(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	aliceUser := "palice" + suffix
	bobUser := "pbob" + suffix
	alice := mustRegister(t, h, aliceUser, aliceUser+"@example.com", "password12")
	bob := mustRegister(t, h, bobUser, bobUser+"@example.com", "password12")
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token(aliceUser + "@example.com")}).StatusCode != 204 {
		t.Fatal("verify alice")
	}
	if do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": h.mail.Token(bobUser + "@example.com")}).StatusCode != 204 {
		t.Fatal("verify bob")
	}

	img := do(t, h.ts, http.MethodPost, "/v1/posts/images", alice.str("accessToken"), map[string]string{"mimeType": "image/png"})
	if img.StatusCode != 200 || img.str("objectKey") == "" {
		t.Fatalf("image session: %d %s", img.StatusCode, img.body)
	}
	up := img.str("uploadUrl")
	if i := strings.Index(up, "/v1/uploads/"); i >= 0 {
		up = up[i:]
	}
	req, _ := http.NewRequest(http.MethodPut, h.ts.URL+up, bytes.NewReader([]byte{137, 80, 78, 71, 13, 10, 26, 10}))
	req.Header.Set("Content-Type", "image/png")
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	put.Body.Close()
	if put.StatusCode != 204 {
		t.Fatalf("put image: %d", put.StatusCode)
	}

	chID := alice.str("user", "defaultChannel", "id")
	handle := alice.str("user", "defaultChannel", "handle")
	created := do(t, h.ts, http.MethodPost, "/v1/posts", alice.str("accessToken"), map[string]any{
		"body":            "今天吃了面",
		"channelId":       chID,
		"imageObjectKeys": []string{img.str("objectKey")},
	})
	if created.StatusCode != 201 {
		t.Fatalf("create post: %d %s", created.StatusCode, created.body)
	}
	postID := created.str("id")
	if created.int("viewCount") != 0 {
		t.Fatalf("new post views: %s", created.body)
	}
	if len(created.slice("imageUrls")) != 1 {
		t.Fatalf("image urls: %s", created.body)
	}

	userless := do(t, h.ts, http.MethodPost, "/v1/posts", alice.str("accessToken"), map[string]string{"body": "仅出现在主页"})
	if userless.StatusCode != 201 {
		t.Fatalf("user post: %d %s", userless.StatusCode, userless.body)
	}

	got := do(t, h.ts, http.MethodGet, "/v1/posts/"+postID, "", nil)
	if got.StatusCode != 200 || got.str("body") != "今天吃了面" || got.int("viewCount") != 1 {
		t.Fatalf("get post: %d %s", got.StatusCode, got.body)
	}
	uposts := do(t, h.ts, http.MethodGet, "/v1/users/"+aliceUser+"/posts", "", nil)
	if uposts.StatusCode != 200 || len(uposts.slice("items")) != 2 {
		t.Fatalf("user posts: %d %s", uposts.StatusCode, uposts.body)
	}
	cposts := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle+"/posts", "", nil)
	if cposts.StatusCode != 200 || len(cposts.slice("items")) != 1 {
		t.Fatalf("channel posts: %d %s", cposts.StatusCode, cposts.body)
	}

	if do(t, h.ts, http.MethodPost, "/v1/channels/"+handle+"/subscribe", bob.str("accessToken"), nil).StatusCode != 204 {
		t.Fatal("subscribe")
	}
	feed := do(t, h.ts, http.MethodGet, "/v1/feed/posts", bob.str("accessToken"), nil)
	if feed.StatusCode != 200 || len(feed.slice("items")) != 1 {
		t.Fatalf("feed: %d %s", feed.StatusCode, feed.body)
	}

	if do(t, h.ts, http.MethodPost, "/v1/posts/"+postID+"/like", bob.str("accessToken"), nil).StatusCode != 204 {
		t.Fatal("like")
	}
	cmt := do(t, h.ts, http.MethodPost, "/v1/posts/"+postID+"/comments", bob.str("accessToken"), map[string]string{"body": "好香"})
	if cmt.StatusCode != 201 {
		t.Fatalf("comment: %d %s", cmt.StatusCode, cmt.body)
	}
	again := do(t, h.ts, http.MethodGet, "/v1/posts/"+postID, bob.str("accessToken"), nil)
	if again.int("likeCount") != 1 || again.int("commentCount") != 1 || !again.bool("liked") {
		t.Fatalf("counts: %s", again.body)
	}

	page := do(t, h.ts, http.MethodGet, "/u/"+aliceUser, "", nil)
	if page.StatusCode != 200 || !strings.Contains(page.body, "/v1/users/") {
		t.Fatalf("user page: %d", page.StatusCode)
	}
	chPage := do(t, h.ts, http.MethodGet, "/c/"+handle, "", nil)
	if chPage.StatusCode != 200 || !strings.Contains(chPage.body, "ch-avatar") || !strings.Contains(chPage.body, "media-title") || !strings.Contains(chPage.body, `title="${esc(title)}"`) || !strings.Contains(chPage.body, "sizeLabel") || !strings.Contains(chPage.body, "ch-section") || !strings.Contains(chPage.body, "timeAgo") || !strings.Contains(chPage.body, "publishBtn") {
		t.Fatalf("channel page: %d", chPage.StatusCode)
	}
	pPage := do(t, h.ts, http.MethodGet, "/post/"+postID, "", nil)
	if pPage.StatusCode != 200 {
		t.Fatalf("post page: %d", pPage.StatusCode)
	}
	home := do(t, h.ts, http.MethodGet, "/", "", nil)
	if home.StatusCode != 200 || !strings.Contains(home.body, "/compose") {
		t.Fatalf("home: %d", home.StatusCode)
	}
	if do(t, h.ts, http.MethodGet, "/compose", "", nil).StatusCode != 200 {
		t.Fatal("compose page")
	}
	if do(t, h.ts, http.MethodGet, "/feed", "", nil).StatusCode != 200 {
		t.Fatal("feed page")
	}
	css := do(t, h.ts, http.MethodGet, "/static/post-media.css", "", nil)
	if css.StatusCode != 200 || !strings.Contains(css.body, ".post-imgs") || !strings.Contains(css.body, ".post-imgs.n6") {
		t.Fatalf("post-media.css: %d", css.StatusCode)
	}
	js := do(t, h.ts, http.MethodGet, "/static/post-media.js", "", nil)
	if js.StatusCode != 200 || !strings.Contains(js.body, "renderPostImages") || !strings.Contains(js.body, "n6") {
		t.Fatalf("post-media.js: %d", js.StatusCode)
	}
	if !strings.Contains(page.body, "/static/post-media.css") || !strings.Contains(page.body, "renderPostImages") {
		t.Fatal("user page missing image grid")
	}

	carol := mustRegister(t, h, "pcarol"+suffix, "pcarol"+suffix+"@example.com", "password12")
	if do(t, h.ts, http.MethodPost, "/v1/posts", carol.str("accessToken"), map[string]string{"body": "未验证"}).StatusCode != 403 {
		t.Fatal("unverified should not post")
	}
}
