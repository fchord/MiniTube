package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"minitube/api/internal/store"
	"minitube/api/internal/totp"
)

func seedSiteAdmin(t *testing.T, password string) harness {
	t.Helper()
	h := setup(t)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.store.UpsertAdminAccount(context.Background(), store.AdminAccount{
		PasswordHash: string(hash),
	}); err != nil {
		t.Fatal(err)
	}
	return setup(t)
}

func totpNow(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.Code(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func TestAdminPagesServe(t *testing.T) {
	h := setup(t)
	got := do(t, h.ts, http.MethodGet, "/admin", "", nil)
	if got.StatusCode != 200 || !strings.Contains(got.body, "MiniTube") {
		t.Fatalf("/admin: %d", got.StatusCode)
	}
	denied := do(t, h.ts, http.MethodGet, "/admin/setup", "", nil)
	if denied.StatusCode != http.StatusNotFound {
		t.Fatalf("public /admin/setup: %d %s", denied.StatusCode, denied.body)
	}
	lan := doLAN(t, h.ts, http.MethodGet, "/admin/setup", "", nil)
	if lan.StatusCode != 200 || !strings.Contains(lan.body, "口令") {
		t.Fatalf("LAN /admin/setup: %d", lan.StatusCode)
	}
}

func TestAdminLoginRequiresTOTP(t *testing.T) {
	pass := "admin-pass-1"
	h := seedSiteAdmin(t, pass)
	st := do(t, h.ts, http.MethodGet, "/v1/admin/login-status", "", nil)
	if st.StatusCode != 200 || st.bool("totpEnrolled") {
		t.Fatalf("status: %d %s", st.StatusCode, st.body)
	}
	got := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{
		"password": pass, "totp": "000000",
	})
	if got.StatusCode != http.StatusUnauthorized || got.str("code") != "totp_not_enrolled" {
		t.Fatalf("login: %d %s", got.StatusCode, got.body)
	}
}

func TestAdminSetupEnrollAndSitePatch(t *testing.T) {
	pass := "admin-pass-2"
	h := seedSiteAdmin(t, pass)
	st := doLAN(t, h.ts, http.MethodGet, "/v1/admin/setup", "", nil)
	if st.StatusCode != 200 || st.bool("totpEnrolled") {
		t.Fatalf("setup status: %d %s", st.StatusCode, st.body)
	}
	login := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/login", "", map[string]string{"password": pass})
	if login.StatusCode != 200 || login.str("token") == "" {
		t.Fatalf("setup login: %d %s", login.StatusCode, login.body)
	}
	setupTok := login.str("token")
	begin := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/begin", setupTok, map[string]any{})
	if begin.StatusCode != 200 || begin.str("secret") == "" || begin.str("qrPng") == "" {
		t.Fatalf("begin: %d %s", begin.StatusCode, begin.body)
	}
	secret := begin.str("secret")
	bad := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/confirm", setupTok, map[string]string{"code": "000000"})
	if bad.StatusCode != http.StatusUnauthorized || bad.str("code") != "bad_totp" {
		t.Fatalf("bad confirm: %d %s", bad.StatusCode, bad.body)
	}
	ok := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/confirm", setupTok, map[string]string{"code": totpNow(t, secret)})
	if ok.StatusCode != 200 || !ok.bool("totpEnrolled") {
		t.Fatalf("confirm: %d %s", ok.StatusCode, ok.body)
	}
	adm := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": totpNow(t, secret)})
	if adm.StatusCode != 200 || adm.str("token") == "" {
		t.Fatalf("admin login: %d %s", adm.StatusCode, adm.body)
	}
	tok := adm.str("token")
	patched := do(t, h.ts, http.MethodPatch, "/v1/admin/site", tok, map[string]any{
		"shortsEngine":     "webcodecs",
		"mediaEdgeEnabled": true,
	})
	if patched.StatusCode != 200 || patched.str("shortsEngine") != "webcodecs" || !patched.bool("mediaEdgeEnabled") {
		t.Fatalf("patch: %d %s", patched.StatusCode, patched.body)
	}
	edges := patched.slice("mediaEdges")
	if len(edges) == 0 {
		t.Fatalf("expected ctc edge: %s", patched.body)
	}
	id, _ := edges[0].(map[string]any)["id"].(string)
	if id != "ctc" {
		t.Fatalf("edge id: %s", patched.body)
	}
	off := do(t, h.ts, http.MethodPatch, "/v1/admin/site", tok, map[string]any{
		"shortsEngine":     "legacy",
		"mediaEdgeEnabled": false,
	})
	if off.StatusCode != 200 || off.bool("mediaEdgeEnabled") || off.str("shortsEngine") != "legacy" {
		t.Fatalf("disable: %d %s", off.StatusCode, off.body)
	}
}

func TestAdminRebindInvalidatesOldTOTP(t *testing.T) {
	pass := "admin-pass-3"
	h := seedSiteAdmin(t, pass)
	setupTok := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/login", "", map[string]string{"password": pass}).str("token")
	begin := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/begin", setupTok, map[string]any{})
	oldSecret := begin.str("secret")
	if got := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/confirm", setupTok, map[string]string{"code": totpNow(t, oldSecret)}); got.StatusCode != 200 {
		t.Fatalf("enroll: %d %s", got.StatusCode, got.body)
	}
	setupTok = doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/login", "", map[string]string{"password": pass, "totp": totpNow(t, oldSecret)}).str("token")
	rebind := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/begin", setupTok, map[string]any{})
	newSecret := rebind.str("secret")
	if newSecret == "" || newSecret == oldSecret {
		t.Fatalf("rebind secret: %s", rebind.body)
	}
	denied := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": totpNow(t, oldSecret)})
	if denied.str("code") != "totp_not_enrolled" {
		t.Fatalf("old totp still usable: %d %s", denied.StatusCode, denied.body)
	}
	if got := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/totp/confirm", setupTok, map[string]string{"code": totpNow(t, newSecret)}); got.StatusCode != 200 {
		t.Fatalf("rebind confirm: %d %s", got.StatusCode, got.body)
	}
	login := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": totpNow(t, newSecret)})
	if login.StatusCode != 200 {
		t.Fatalf("login after rebind: %d %s", login.StatusCode, login.body)
	}
	oldLogin := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": totpNow(t, oldSecret)})
	if oldLogin.StatusCode == 200 {
		t.Fatal("old totp accepted after rebind")
	}
}

func TestAdminSetupChangePassword(t *testing.T) {
	pass := "admin-pass-4"
	h := seedSiteAdmin(t, pass)
	setupTok := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/login", "", map[string]string{"password": pass}).str("token")
	bad := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/password", setupTok, map[string]string{
		"currentPassword": "wrong", "newPassword": "admin-pass-5",
	})
	if bad.str("code") != "bad_password" {
		t.Fatalf("wrong current: %d %s", bad.StatusCode, bad.body)
	}
	ok := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/password", setupTok, map[string]string{
		"currentPassword": pass, "newPassword": "admin-pass-5",
	})
	if ok.StatusCode != 200 {
		t.Fatalf("change: %d %s", ok.StatusCode, ok.body)
	}
	again := doLAN(t, h.ts, http.MethodPost, "/v1/admin/setup/login", "", map[string]string{"password": "admin-pass-5"})
	if again.StatusCode != 200 {
		t.Fatalf("login new pass: %d %s", again.StatusCode, again.body)
	}
}

func TestAdminSetupForbiddenFromPublic(t *testing.T) {
	h := seedSiteAdmin(t, "admin-pass-lan")
	cf := doEx(t, h.ts, http.MethodGet, "/v1/admin/setup", "", nil, "192.168.43.111", map[string]string{
		"X-Real-IP": "192.168.43.10", "CF-Connecting-IP": "203.0.113.90",
	})
	if cf.StatusCode != http.StatusNotFound {
		t.Fatalf("CF: %d %s", cf.StatusCode, cf.body)
	}
	pub := doEx(t, h.ts, http.MethodGet, "/v1/admin/setup", "", nil, "minitube.19121122.xyz", map[string]string{
		"X-Real-IP": "192.168.43.10",
	})
	if pub.StatusCode != http.StatusNotFound {
		t.Fatalf("public host: %d %s", pub.StatusCode, pub.body)
	}
	wan := doEx(t, h.ts, http.MethodGet, "/v1/admin/setup", "", nil, "192.168.43.111", map[string]string{
		"X-Real-IP": "203.0.113.91",
	})
	if wan.StatusCode != http.StatusNotFound {
		t.Fatalf("WAN IP: %d %s", wan.StatusCode, wan.body)
	}
	pod := doEx(t, h.ts, http.MethodGet, "/v1/admin/setup", "", nil, "192.168.43.111", map[string]string{
		"X-Real-IP": "10.244.0.1",
	})
	if pod.StatusCode != 200 {
		t.Fatalf("pod SNAT: %d %s", pod.StatusCode, pod.body)
	}
}

func TestAdminLoginRateLimited(t *testing.T) {
	pass := "admin-pass-rate"
	h := seedSiteAdmin(t, pass)
	var last obj
	for i := 0; i < 8; i++ {
		last = do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": "000000"})
		if last.str("code") != "totp_not_enrolled" {
			t.Fatalf("attempt %d: %d %s", i+1, last.StatusCode, last.body)
		}
	}
	blocked := do(t, h.ts, http.MethodPost, "/v1/admin/login", "", map[string]string{"password": pass, "totp": "000000"})
	if blocked.StatusCode != http.StatusTooManyRequests || blocked.str("code") != "locked" {
		t.Fatalf("rate: %d %s", blocked.StatusCode, blocked.body)
	}
	st := do(t, h.ts, http.MethodGet, "/v1/admin/login-status", "", nil)
	if !st.bool("locked") || st.int("retryAfter") < 1 {
		t.Fatalf("status lock: %s", st.body)
	}
}
