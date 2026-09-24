package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOriginMediaURLRewritesPublicHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://192.168.43.111:8080/v1/videos/x/playback", nil)
	got := sameOriginMediaURL(r, "https://minitube.19121122.xyz/v1/media/hls/abc/master.m3u8?v=1", nil)
	want := "http://192.168.43.111:8080/v1/media/hls/abc/master.m3u8?v=1"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	live := sameOriginMediaURL(r, "https://minitube.19121122.xyz/v1/live/abc/hls/index.m3u8", nil)
	if live != "http://192.168.43.111:8080/v1/live/abc/hls/index.m3u8" {
		t.Fatalf("live rewrite: %q", live)
	}
}

func TestSameOriginMediaURLLeavesOtherHosts(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://192.168.43.111:8080/", nil)
	ext := "https://cdn.example/thumb.jpg"
	if sameOriginMediaURL(r, ext, nil) != ext {
		t.Fatal("rewrote non-media url")
	}
	if sameOriginMediaURL(r, "", nil) != "" {
		t.Fatal("empty url")
	}
}

func TestSameOriginMediaURLKeepsEdgeHost(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "https://minitube.19121122.xyz/v1/videos/x/playback", nil)
	abs := "https://ctc.minitube.19121122.xyz:18080/v1/media/hls/abc/master.m3u8"
	got := sameOriginMediaURL(r, abs, []string{"ctc.minitube.19121122.xyz"})
	if got != abs {
		t.Fatalf("rewrote edge url: %q", got)
	}
}

func TestHostnameOnlyStripsPort(t *testing.T) {
	if hostnameOnly("ctc.minitube.19121122.xyz:18080") != "ctc.minitube.19121122.xyz" {
		t.Fatal(hostnameOnly("ctc.minitube.19121122.xyz:18080"))
	}
}

func TestRequestOriginForwardedProto(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://minitube.19121122.xyz/v1/videos/x/playback", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := requestOrigin(r); got != "https://minitube.19121122.xyz" {
		t.Fatalf("forwarded proto: %q", got)
	}
	tlsReq := httptest.NewRequest(http.MethodGet, "https://minitube.19121122.xyz/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if got := requestOrigin(tlsReq); got != "https://minitube.19121122.xyz" {
		t.Fatalf("tls origin: %q", got)
	}
}
