package httpapi

import (
	"strings"
	"testing"
)

func TestClientCapAsset(t *testing.T) {
	js, err := watchHTML.ReadFile("client-cap.js")
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	for _, need := range []string{
		"mtClientCap",
		"fetchPlayback",
		"hardwareAcceleration",
		"prefer-hardware",
		"mediaCapabilities",
		"hwCodecs",
		"h264",
		"hevc",
		"av1",
		"vp9",
		"VideoDecoder",
		"getHighEntropyValues",
		"av01.0.12M.08",
		"hevcUsableForHLS",
	} {
		if !strings.Contains(body, need) {
			t.Fatalf("client-cap.js missing %q", need)
		}
	}
	if strings.Contains(body, "av01.0.0CM.08") || strings.Contains(body, "av01.0.0DM.08") {
		t.Fatal("AV1 probe still uses Chrome-unparseable hex level 0C/0D")
	}
}
