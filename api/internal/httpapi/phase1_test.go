package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"minitube/api/internal/auth"
	"minitube/api/internal/config"
	"minitube/api/internal/db"
	"minitube/api/internal/httpapi"
	"minitube/api/internal/mailer"
	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

type harness struct {
	ts      *httptest.Server
	mail    *mailer.Memory
	store   *store.Postgres
	uploads *storage.Local
}

func setup(t *testing.T) harness {
	t.Helper()
	t.Setenv("LIVE_HLS_DIR", t.TempDir())
	t.Setenv("SRS_HLS_DIR", t.TempDir())
	t.Setenv("SRS_HOOK_SECRET", "dev-srs-hook")
	cfg := config.FromEnv()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dir := t.TempDir()
	uploads, err := storage.NewLocal(dir, "http://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	mail := mailer.NewMemory()
	st := store.New(pool)
	h := httpapi.New(cfg, st, auth.NewJWT(cfg.JWTSecret, time.Hour), mail, uploads)
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	uploads.SetBaseURL(ts.URL)
	return harness{ts: ts, mail: mail, store: st, uploads: uploads}
}

func TestPhase1Acceptance(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	aliceUser := "alice" + suffix
	bobUser := "bob" + suffix
	aliceMail := aliceUser + "@example.com"
	bobMail := bobUser + "@example.com"

	alice := mustRegister(t, h, aliceUser, aliceMail, "password12")
	if alice.str("user", "defaultChannel", "handle") == "" {
		t.Fatal("missing default channel")
	}
	if alice.bool("user", "emailVerified") {
		t.Fatal("email should not be verified yet")
	}

	tok := h.mail.Token(aliceMail)
	if tok == "" {
		t.Fatal("missing verification token")
	}
	res := do(t, h.ts, http.MethodPost, "/v1/auth/verify-email", "", map[string]string{"token": tok})
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("verify: %d %s", res.StatusCode, res.body)
	}

	me := do(t, h.ts, http.MethodGet, "/v1/me", alice.str("accessToken"), nil)
	if me.StatusCode != 200 || !me.bool("emailVerified") {
		t.Fatalf("me verified: %d %s", me.StatusCode, me.body)
	}

	patched := do(t, h.ts, http.MethodPatch, "/v1/me", alice.str("accessToken"), map[string]string{"nickname": "Alice"})
	if patched.StatusCode != 200 || patched.str("nickname") != "Alice" {
		t.Fatalf("patch me: %d %s", patched.StatusCode, patched.body)
	}

	food := do(t, h.ts, http.MethodPost, "/v1/channels", alice.str("accessToken"), map[string]any{
		"name": "美食", "handle": "food" + suffix, "description": "food", "tags": []string{"food"},
	})
	if food.StatusCode != 201 {
		t.Fatalf("create channel: %d %s", food.StatusCode, food.body)
	}

	bob := mustRegister(t, h, bobUser, bobMail, "password12")
	handle := alice.str("user", "defaultChannel", "handle")
	sub := do(t, h.ts, http.MethodPost, "/v1/channels/"+handle+"/subscribe", bob.str("accessToken"), nil)
	if sub.StatusCode != http.StatusNoContent {
		t.Fatalf("subscribe: %d %s", sub.StatusCode, sub.body)
	}
	again := do(t, h.ts, http.MethodPost, "/v1/channels/"+handle+"/subscribe", bob.str("accessToken"), nil)
	if again.StatusCode != http.StatusNoContent {
		t.Fatalf("subscribe idempotent: %d", again.StatusCode)
	}

	ch := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle, bob.str("accessToken"), nil)
	if ch.StatusCode != 200 || ch.int("subscriberCount") != 1 || !ch.bool("subscribed") {
		t.Fatalf("channel after sub: %d %s", ch.StatusCode, ch.body)
	}
	if _, ok := ch.m["videoCount"]; !ok || ch.m["shortCount"] == nil || ch.m["liveCount"] == nil || ch.m["postCount"] == nil {
		t.Fatalf("channel missing counts: %s", ch.body)
	}

	anon := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle, "", nil)
	if anon.StatusCode != 200 || anon.int("subscriberCount") != 1 {
		t.Fatalf("anon channel: %d %s", anon.StatusCode, anon.body)
	}
	if _, ok := anon.m["subscribed"]; ok {
		t.Fatalf("anon should omit subscribed: %s", anon.body)
	}

	subs := do(t, h.ts, http.MethodGet, "/v1/me/subscriptions", bob.str("accessToken"), nil)
	if subs.StatusCode != 200 || len(subs.slice("items")) != 1 {
		t.Fatalf("subscriptions: %d %s", subs.StatusCode, subs.body)
	}

	pub := do(t, h.ts, http.MethodGet, "/v1/users/"+aliceUser, "", nil)
	if pub.StatusCode != 200 || pub.str("username") != aliceUser {
		t.Fatalf("public user: %d %s", pub.StatusCode, pub.body)
	}
	chs := do(t, h.ts, http.MethodGet, "/v1/users/"+aliceUser+"/channels", "", nil)
	if chs.StatusCode != 200 || len(chs.slice("items")) != 2 {
		t.Fatalf("user channels: %d %s", chs.StatusCode, chs.body)
	}

	unsub := do(t, h.ts, http.MethodDelete, "/v1/channels/"+handle+"/subscribe", bob.str("accessToken"), nil)
	if unsub.StatusCode != http.StatusNoContent {
		t.Fatalf("unsubscribe: %d %s", unsub.StatusCode, unsub.body)
	}
	ch2 := do(t, h.ts, http.MethodGet, "/v1/channels/"+handle, bob.str("accessToken"), nil)
	if ch2.int("subscriberCount") != 0 || ch2.bool("subscribed") {
		t.Fatalf("after unsub: %s", ch2.body)
	}

	session := do(t, h.ts, http.MethodPost, "/v1/me/avatar", alice.str("accessToken"), map[string]string{"mimeType": "image/png"})
	if session.StatusCode != 200 || session.str("uploadUrl") == "" {
		t.Fatalf("avatar session: %d %s", session.StatusCode, session.body)
	}
	upPath := session.str("uploadUrl")
	if i := strings.Index(upPath, "/v1/uploads/"); i >= 0 {
		upPath = upPath[i:]
	}
	put, err := http.NewRequest(http.MethodPut, h.ts.URL+upPath, bytes.NewReader([]byte{137, 80, 78, 71}))
	if err != nil {
		t.Fatal(err)
	}
	put.Header.Set("Content-Type", "image/png")
	putRes, err := http.DefaultClient.Do(put)
	if err != nil {
		t.Fatal(err)
	}
	putRes.Body.Close()
	if putRes.StatusCode != http.StatusNoContent {
		t.Fatalf("put avatar: %d", putRes.StatusCode)
	}
	done := do(t, h.ts, http.MethodPost, "/v1/me/avatar", alice.str("accessToken"), map[string]any{"complete": true})
	if done.StatusCode != 200 {
		t.Fatalf("complete avatar: %d %s", done.StatusCode, done.body)
	}
	me2 := do(t, h.ts, http.MethodGet, "/v1/me", alice.str("accessToken"), nil)
	if me2.str("avatarUrl") == "" {
		t.Fatalf("avatar url missing: %s", me2.body)
	}

	fav := do(t, h.ts, http.MethodGet, "/v1/me/favorites", alice.str("accessToken"), nil)
	if fav.StatusCode != 200 || len(fav.slice("items")) != 1 {
		t.Fatalf("favorites: %d %s", fav.StatusCode, fav.body)
	}

	vid := do(t, h.ts, http.MethodPost, "/v1/videos", alice.str("accessToken"), map[string]string{"title": "x"})
	if vid.StatusCode != http.StatusBadRequest {
		t.Fatalf("videos missing channel: %d %s", vid.StatusCode, vid.body)
	}

	dup := do(t, h.ts, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"username": aliceUser, "email": "other" + suffix + "@example.com", "password": "password12",
	})
	if dup.StatusCode != http.StatusConflict {
		t.Fatalf("dup username: %d %s", dup.StatusCode, dup.body)
	}

	logout := do(t, h.ts, http.MethodPost, "/v1/auth/logout", alice.str("accessToken"), nil)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d %s", logout.StatusCode, logout.body)
	}
	denied := do(t, h.ts, http.MethodGet, "/v1/me", alice.str("accessToken"), nil)
	if denied.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout: %d %s", denied.StatusCode, denied.body)
	}

	login := do(t, h.ts, http.MethodPost, "/v1/auth/login", "", map[string]string{
		"identifier": aliceMail, "password": "password12",
	})
	if login.StatusCode != 200 {
		t.Fatalf("login: %d %s", login.StatusCode, login.body)
	}

	badPw := do(t, h.ts, http.MethodPut, "/v1/me/password", login.str("accessToken"), map[string]string{
		"currentPassword": "wrong-pass", "newPassword": "password99",
	})
	if badPw.StatusCode != http.StatusBadRequest {
		t.Fatalf("wrong current password: %d %s", badPw.StatusCode, badPw.body)
	}
	shortPw := do(t, h.ts, http.MethodPut, "/v1/me/password", login.str("accessToken"), map[string]string{
		"currentPassword": "password12", "newPassword": "short",
	})
	if shortPw.StatusCode != http.StatusBadRequest {
		t.Fatalf("short password: %d %s", shortPw.StatusCode, shortPw.body)
	}
	changed := do(t, h.ts, http.MethodPut, "/v1/me/password", login.str("accessToken"), map[string]string{
		"currentPassword": "password12", "newPassword": "password99",
	})
	if changed.StatusCode != http.StatusNoContent {
		t.Fatalf("change password: %d %s", changed.StatusCode, changed.body)
	}
	oldLogin := do(t, h.ts, http.MethodPost, "/v1/auth/login", "", map[string]string{
		"identifier": aliceMail, "password": "password12",
	})
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password still works: %d %s", oldLogin.StatusCode, oldLogin.body)
	}
	newLogin := do(t, h.ts, http.MethodPost, "/v1/auth/login", "", map[string]string{
		"identifier": aliceMail, "password": "password99",
	})
	if newLogin.StatusCode != 200 {
		t.Fatalf("new password login: %d %s", newLogin.StatusCode, newLogin.body)
	}

	home := do(t, h.ts, http.MethodGet, "/", "", nil)
	if home.StatusCode != 200 || !strings.Contains(home.body, "/static/account.js?v=") || !strings.Contains(home.body, "/static/token.js?v=") {
		t.Fatalf("home account: %d", home.StatusCode)
	}
	tokjs := do(t, h.ts, http.MethodGet, "/static/token.js", "", nil)
	if tokjs.StatusCode != 200 || !strings.Contains(tokjs.body, "localStorage") || !strings.Contains(tokjs.body, "mt.accessToken") {
		t.Fatalf("token js: %d", tokjs.StatusCode)
	}
	chPage := do(t, h.ts, http.MethodGet, "/c/missinghandle", "", nil)
	if chPage.StatusCode != 200 || !strings.Contains(chPage.body, "/static/token.js?v=") {
		t.Fatalf("channel page token: %d", chPage.StatusCode)
	}
	acc := do(t, h.ts, http.MethodGet, "/static/account.js", "", nil)
	if acc.StatusCode != 200 || !strings.Contains(acc.body, "nameInitials") || !strings.Contains(acc.body, "我的频道") || strings.Contains(acc.body, "进入我的频道") || !strings.Contains(acc.body, "href=\"/settings\"") || !strings.Contains(acc.body, "退出账号") {
		t.Fatalf("account js: %d", acc.StatusCode)
	}
	loginPage := do(t, h.ts, http.MethodGet, "/login", "", nil)
	if loginPage.StatusCode != 200 || !strings.Contains(loginPage.body, "/v1/auth/login") || !strings.Contains(loginPage.body, "/register") {
		t.Fatalf("login page: %d", loginPage.StatusCode)
	}
	regPage := do(t, h.ts, http.MethodGet, "/register", "", nil)
	if regPage.StatusCode != 200 || !strings.Contains(regPage.body, "/v1/auth/register") {
		t.Fatalf("register page: %d", regPage.StatusCode)
	}
	settingsPage := do(t, h.ts, http.MethodGet, "/settings", "", nil)
	if settingsPage.StatusCode != 200 || !strings.Contains(settingsPage.body, "/v1/me/password") || !strings.Contains(settingsPage.body, "邮箱是否已验证") || !strings.Contains(settingsPage.body, "currentPassword") {
		t.Fatalf("settings page: %d", settingsPage.StatusCode)
	}
	if strings.Contains(settingsPage.body, "location.reload") {
		t.Fatal("settings page should not reload after avatar upload")
	}
}

func mustRegister(t *testing.T, h harness, username, email, password string) obj {
	t.Helper()
	res := do(t, h.ts, http.MethodPost, "/v1/auth/register", "", map[string]string{
		"username": username, "email": email, "password": password,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: %d %s", username, res.StatusCode, res.body)
	}
	return res
}

type obj struct {
	StatusCode int
	body       string
	m          map[string]any
}

func (o obj) str(path ...string) string {
	v := o.val(path...)
	s, _ := v.(string)
	return s
}

func (o obj) bool(path ...string) bool {
	v, _ := o.val(path...).(bool)
	return v
}

func (o obj) int(path ...string) int {
	switch v := o.val(path...).(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func (o obj) slice(path ...string) []any {
	v, _ := o.val(path...).([]any)
	return v
}

func (o obj) val(path ...string) any {
	var cur any = o.m
	for _, p := range path {
		m, _ := cur.(map[string]any)
		cur = m[p]
	}
	return cur
}

func do(t *testing.T, ts *httptest.Server, method, path, token string, body any) obj {
	t.Helper()
	return doEx(t, ts, method, path, token, body, "", nil)
}

func doLAN(t *testing.T, ts *httptest.Server, method, path, token string, body any) obj {
	t.Helper()
	return doEx(t, ts, method, path, token, body, "192.168.43.111", map[string]string{"X-Real-IP": "192.168.43.10"})
}

func doEx(t *testing.T, ts *httptest.Server, method, path, token string, body any, host string, headers map[string]string) obj {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
		req.Header.Set("Host", host)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := obj{StatusCode: res.StatusCode, body: string(raw), m: map[string]any{}}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		_ = json.Unmarshal(raw, &out.m)
	}
	return out
}

func TestMain(m *testing.M) {
	if os.Getenv("DATABASE_URL") == "" {
		_ = os.Setenv("DATABASE_URL", "postgres://minitube:minitube@127.0.0.1:5433/minitube?sslmode=disable")
	}
	os.Exit(m.Run())
}
