package httpapi

import (
	"strings"
	"testing"
)

func TestTsFmp4Asset(t *testing.T) {
	js, err := watchHTML.ReadFile("ts-fmp4.js")
	if err != nil {
		t.Fatal(err)
	}
	body := string(js)
	for _, need := range []string{
		"mtTsFmp4",
		"splitAnnexB",
		"flushPes",
		"remuxSegment",
		"parseMaster",
		"hevcFirstSlice",
		"rfc6381Hevc",
		"moovAV",
		"moovAudio",
		"moovVideo",
		"parseHevcSps",
		"hevcIrap",
		"bitDepthLumaMinus8",
		"0, 0x10, 0, 0, 0, 0",
		"stale",
		"timestampOffset",
		"hevc 10bit",
		"tsOffApplied",
		"segStartAt",
		"freezeFrame",
		"height <= want",
		"timeCovered",
		"aimAt",
		"seekSeq",
		"onAudioGate",
		"seek: function",
		"[mt-ts]",
	} {
		if !strings.Contains(body, need) {
			t.Fatalf("ts-fmp4.js missing %q", need)
		}
	}
}
