package httpapi_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPublicSiteDefaultLegacy(t *testing.T) {
	h := setup(t)
	got := do(t, h.ts, http.MethodGet, "/v1/public/site", "", nil)
	if got.StatusCode != 200 || got.str("shortsEngine") != "legacy" {
		t.Fatalf("public site: %d %s", got.StatusCode, got.body)
	}
	if got.bool("mediaEdgeEnabled") {
		t.Fatal("media edges should default off")
	}
	if got.slice("mediaEdges") == nil {
		t.Fatal("mediaEdges should be an array")
	}
}

func TestAdminSiteForbiddenWithoutAllowlist(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "adm" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	got := do(t, h.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]string{"shortsEngine": "webcodecs"})
	if got.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", got.StatusCode, got.body)
	}
}

func TestAdminSitePatchWhenAllowlisted(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	user := "admx" + suffix
	email := user + "@example.com"
	alice := mustRegister(t, h, user, email, "password12")
	id := alice.str("user", "id")
	if id == "" {
		t.Fatal("missing user id")
	}
	t.Setenv("ADMIN_USER_IDS", id)
	h2 := setup(t)
	got := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]string{"shortsEngine": "webcodecs"})
	if got.StatusCode != 200 || got.str("shortsEngine") != "webcodecs" {
		t.Fatalf("admin patch: %d %s", got.StatusCode, got.body)
	}
	pub := do(t, h2.ts, http.MethodGet, "/v1/public/site", "", nil)
	if pub.str("shortsEngine") != "webcodecs" {
		t.Fatalf("public after patch: %s", pub.body)
	}
	reset := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]string{"shortsEngine": "legacy"})
	if reset.StatusCode != 200 || reset.str("shortsEngine") != "legacy" {
		t.Fatalf("reset: %d %s", reset.StatusCode, reset.body)
	}
}

func TestAdminMediaEdgesPatch(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "edg"+suffix, "edg"+suffix+"@example.com", "password12")
	id := alice.str("user", "id")
	t.Setenv("ADMIN_USER_IDS", id)
	h2 := setup(t)
	t.Cleanup(func() {
		do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{
			"mediaEdgeEnabled": false, "mediaEdges": []any{},
		})
	})
	got := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{
		"mediaEdgeEnabled": true,
		"mediaEdges": []map[string]any{
			{"id": "ctc", "base": "https://ctc.minitube.19121122.xyz:18080", "regions": []string{"CN"}},
		},
	})
	if got.StatusCode != 200 || !got.bool("mediaEdgeEnabled") {
		t.Fatalf("enable edges: %d %s", got.StatusCode, got.body)
	}
	edges := got.slice("mediaEdges")
	if len(edges) != 1 {
		t.Fatalf("edges: %s", got.body)
	}
	pub := do(t, h2.ts, http.MethodGet, "/v1/public/site", "", nil)
	if !pub.bool("mediaEdgeEnabled") {
		t.Fatalf("public enabled: %s", pub.body)
	}
	hintReq, err := http.NewRequest(http.MethodGet, h2.ts.URL+"/v1/public/site", nil)
	if err != nil {
		t.Fatal(err)
	}
	hintReq.Header.Set("CF-IPCountry", "CN")
	hintRes, err := http.DefaultClient.Do(hintReq)
	if err != nil {
		t.Fatal(err)
	}
	defer hintRes.Body.Close()
	var hintObj obj
	hintObj.StatusCode = hintRes.StatusCode
	raw, _ := io.ReadAll(hintRes.Body)
	hintObj.body = string(raw)
	hintObj.m = map[string]any{}
	_ = json.Unmarshal(raw, &hintObj.m)
	if hintObj.str("mediaHint") != "CN" {
		t.Fatalf("hint: %s", hintObj.body)
	}
	off := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{"mediaEdgeEnabled": false})
	if off.StatusCode != 200 || off.bool("mediaEdgeEnabled") {
		t.Fatalf("disable: %d %s", off.StatusCode, off.body)
	}
}

func TestEdgeHostAllowsMediaOnly(t *testing.T) {
	h := setup(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "edgh"+suffix, "edgh"+suffix+"@example.com", "password12")
	t.Setenv("ADMIN_USER_IDS", alice.str("user", "id"))
	h2 := setup(t)
	t.Cleanup(func() {
		do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{"mediaEdges": []any{}})
	})
	patched := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{
		"mediaEdges": []map[string]any{
			{"id": "ctc", "base": "https://ctc.minitube.19121122.xyz:18080", "regions": []string{"CN"}},
		},
	})
	if patched.StatusCode != 200 {
		t.Fatalf("seed edges: %d %s", patched.StatusCode, patched.body)
	}
	probe, err := http.NewRequest(http.MethodGet, h2.ts.URL+"/v1/media/edge-probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	probe.Host = "ctc.minitube.19121122.xyz:18080"
	pres, err := http.DefaultClient.Do(probe)
	if err != nil {
		t.Fatal(err)
	}
	pres.Body.Close()
	if pres.StatusCode != http.StatusNoContent {
		t.Fatalf("probe: %d", pres.StatusCode)
	}
	me, err := http.NewRequest(http.MethodGet, h2.ts.URL+"/v1/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	me.Host = "ctc.minitube.19121122.xyz:18080"
	mres, err := http.DefaultClient.Do(me)
	if err != nil {
		t.Fatal(err)
	}
	mres.Body.Close()
	if mres.StatusCode != http.StatusNotFound {
		t.Fatalf("edge host /v1/me: %d", mres.StatusCode)
	}
}

func TestTestEnvLocksMediaEdge(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	h := setup(t)
	pub := do(t, h.ts, http.MethodGet, "/v1/public/site", "", nil)
	if pub.StatusCode != 200 || pub.str("environment") != "test" || pub.bool("mediaEdgeEnabled") {
		t.Fatalf("public: %d %s", pub.StatusCode, pub.body)
	}
	if !pub.bool("mediaEdgeLocked") || pub.str("mediaEdgeLockReason") != "测试环境暂不支持" {
		t.Fatalf("lock fields: %s", pub.body)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	alice := mustRegister(t, h, "tedge"+suffix, "tedge"+suffix+"@example.com", "password12")
	t.Setenv("ADMIN_USER_IDS", alice.str("user", "id"))
	h2 := setup(t)
	got := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{
		"mediaEdgeEnabled": true,
	})
	if got.StatusCode != http.StatusBadRequest || got.str("code") != "media_edge_locked" {
		t.Fatalf("patch edge: %d %s", got.StatusCode, got.body)
	}
	ok := do(t, h2.ts, http.MethodPatch, "/v1/admin/site", alice.str("accessToken"), map[string]any{
		"shortsEngine": "webcodecs",
	})
	if ok.StatusCode != 200 || ok.str("shortsEngine") != "webcodecs" || ok.bool("mediaEdgeEnabled") {
		t.Fatalf("patch engine: %d %s", ok.StatusCode, ok.body)
	}
}

func TestAdminPageSetupURL(t *testing.T) {
	t.Setenv("APP_ENV", "test")
	t.Setenv("ADMIN_SETUP_URL", "http://192.168.43.111:8081/admin/setup")
	h := setup(t)
	got := do(t, h.ts, http.MethodGet, "/admin", "", nil)
	if got.StatusCode != 200 || !strings.Contains(got.body, "http://192.168.43.111:8081/admin/setup") {
		t.Fatalf("/admin setup url: %d %s", got.StatusCode, got.body)
	}
	if strings.Contains(got.body, "__ADMIN_SETUP_URL__") {
		t.Fatal("placeholder left in admin.html")
	}
}
