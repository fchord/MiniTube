package httpapi_test

import (
	"net/http"
	"testing"
)

func TestHealthzNoContent(t *testing.T) {
	h := setup(t)
	got := do(t, h.ts, http.MethodGet, "/healthz", "", nil)
	if got.StatusCode != http.StatusNoContent {
		t.Fatalf("GET /healthz: %d %s", got.StatusCode, got.body)
	}
	if got.body != "" {
		t.Fatalf("GET /healthz body: %q", got.body)
	}
}
