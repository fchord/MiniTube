package httpapi

import (
	"strings"
	"testing"
)

func TestPlayerAssetsEmbedded(t *testing.T) {
	js, err := watchHTML.ReadFile("player.js")
	if err != nil {
		t.Fatal(err)
	}
	css, err := watchHTML.ReadFile("player.css")
	if err != nil {
		t.Fatal(err)
	}
	page, err := watchHTML.ReadFile("watch.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	for _, need := range []string{`0.5`, `2.5`, `3`, `mt.player.prefs`, `currentLevel`, `DynamicsCompressor`} {
		if !strings.Contains(body, need) {
			t.Fatalf("player.js missing %q", need)
		}
	}
	cssBody := string(css)
	if !strings.Contains(cssBody, ".mt-player") {
		t.Fatal("player.css missing .mt-player")
	}
	if !strings.Contains(cssBody, "border-radius: 6px") || !strings.Contains(cssBody, ".mt-player { border-radius: 4px; }") {
		t.Fatal("player.css should use 6px corners on desktop and 4px on phones")
	}
	if !strings.Contains(cssBody, "opacity .35s ease") {
		t.Fatal("player.css should fade playback chrome")
	}
	if !strings.Contains(cssBody, "mt-menu-sep") || !strings.Contains(cssBody, "padding: 8px 14px 8px 26px") {
		t.Fatal("player.css missing more-menu separators or option indent")
	}
	if !strings.Contains(cssBody, "is-fs-rotate") {
		t.Fatal("player.css missing landscape fullscreen rotate fallback")
	}
	if !strings.Contains(body, "scrubTime") || !strings.Contains(body, "jumpTo") {
		t.Fatal("player.js missing optimistic seek playhead")
	}
	if !strings.Contains(body, "capLevelToPlayerSize") || strings.Contains(body, "backBufferLength: 90000") {
		t.Fatal("player.js should cap quality and not keep a huge MSE back-buffer")
	}
	if !strings.Contains(body, "sameOriginMedia") || !strings.Contains(body, "mtMedia.rewrite") || strings.Contains(body, "progressive: true") {
		t.Fatal("player.js should rewrite HLS via mtMedia and not use progressive TS")
	}
	if !strings.Contains(body, "mt-bigplay") || !strings.Contains(body, "togglePlayQuiet") || !strings.Contains(body, "previewSeek") {
		t.Fatal("player.js missing tap-to-show chrome, center play, or swipe seek")
	}
	if !strings.Contains(body, `lock("landscape")`) || !strings.Contains(body, "orientation.unlock") || !strings.Contains(body, "is-fs-rotate") {
		t.Fatal("player.js should lock landscape on mobile fullscreen and unlock on exit")
	}
	if !strings.Contains(body, "mt-menu-sep") {
		t.Fatal("player.js missing more-menu section separators")
	}
	if strings.Contains(body, "[data-click]") || strings.Contains(body, "data-click") {
		t.Fatal("player.js still toggles playback on video tap")
	}
	html := string(page)
	if strings.Contains(html, "controls") {
		t.Fatal("watch.html still uses native controls")
	}
	if !strings.Contains(html, "/static/player.js") || !strings.Contains(html, "mtPlayer.mount") || !strings.Contains(html, "media-edge.js") {
		t.Fatal("watch.html does not mount custom player")
	}
	if !strings.Contains(html, "@media (max-width: 640px)") || !strings.Contains(html, "padding: 0 0 16px") {
		t.Fatal("watch.html should edge-to-edge the player on narrow phones")
	}
	livePage, err := watchHTML.ReadFile("live.html")
	if err != nil {
		t.Fatal(err)
	}
	liveHTML := string(livePage)
	if strings.Contains(liveHTML, `<video id="player" controls`) {
		t.Fatal("live.html still uses native controls")
	}
	if !strings.Contains(liveHTML, "mtPlayer.mount") || !strings.Contains(body, "setLive") || !strings.Contains(body, "snapSpeedIfLive") {
		t.Fatal("live player is not wired to shared mtPlayer")
	}
}
