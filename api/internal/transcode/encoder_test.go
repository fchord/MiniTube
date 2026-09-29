package transcode

import (
	"errors"
	"os"
	"strings"
	"testing"

	"minitube/api/internal/store"
)

func TestPickRungsCapped(t *testing.T) {
	h := pickRungsCapped(1080, 720)
	if len(h) != 5 || h[0].Height != 144 || h[4].Height != 720 {
		t.Fatalf("720 cap: %#v", h)
	}
	all := pickRungsCapped(1080, 1080)
	if len(all) != 5 || all[4].Height != 720 {
		t.Fatalf("AVC has no 1080: %#v", all)
	}
	low := pickRungsCapped(240, 1080)
	if len(low) != 2 || low[0].Height != 144 || low[1].Height != 240 {
		t.Fatalf("240 source: %#v", low)
	}
}

func TestPickRungsNoUpscalePortrait4K(t *testing.T) {
	t.Setenv("TRANSCODE_MAX_HEIGHT", "2160")
	jobs := pickRungs(2160, 3840)
	var avc, hevc []int
	for _, r := range jobs {
		if r.Family == familyAVC {
			avc = append(avc, r.Height)
		} else {
			hevc = append(hevc, r.Height)
		}
	}
	if len(avc) != 5 || avc[0] != 720 {
		t.Fatalf("avc: %v", avc)
	}
	if len(hevc) != 8 || hevc[0] != 2160 {
		t.Fatalf("hevc should include 2160 for 2160x3840: %v", hevc)
	}
}

func TestVideoEncodeArgsNVENC(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_nvenc")
	t.Setenv("TRANSCODE_PRESET", "hq")
	r := avcLadder[0]
	cpu := strings.Join(videoEncodeArgs("/tmp/src.mp4", r, 1280, 720, true, false, false), " ")
	if !strings.Contains(cpu, "h264_nvenc") {
		t.Fatalf("want nvenc: %s", cpu)
	}
	if strings.Contains(cpu, "libx264") || strings.Contains(cpu, "hwaccel") {
		t.Fatalf("cpu path: %s", cpu)
	}
	hevc := strings.Join(videoEncodeArgs("/tmp/src.mp4", hevcLadder[3], 1280, 720, true, false, false), " ")
	if !strings.Contains(hevc, "hevc_nvenc") || !strings.Contains(hevc, "-cq 28") || !strings.Contains(hevc, "700k") {
		t.Fatalf("hevc: %s", hevc)
	}
	if !strings.Contains(hevc, "-pix_fmt yuv420p") {
		t.Fatalf("hevc should force 8-bit yuv420p: %s", hevc)
	}
	if strings.Contains(hevc, "hwaccel") || strings.Contains(hevc, "scale_cuda") {
		t.Fatalf("hevc should software-decode: %s", hevc)
	}
}

func TestEncodeVideoRungDoesNotTryNVDEC(t *testing.T) {
	body, err := os.ReadFile("transcode.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	if !strings.Contains(s, "cudaTries := []bool{false}") {
		t.Fatal("encodeVideoRung should only software-decode")
	}
	if strings.Contains(s, "[]bool{true, false}") {
		t.Fatal("NVENC must not try CUDA/NVDEC decode first")
	}
}

func TestVideoEncodeArgsQSV(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_qsv")
	args := strings.Join(videoEncodeArgs("/tmp/src.mp4", avcLadder[2], 640, 360, true, false, false), " ")
	if !strings.Contains(args, "h264_qsv") {
		t.Fatalf("want qsv: %s", args)
	}
}

func TestVideoEncodeArgsVAAPI(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_vaapi")
	args := strings.Join(videoEncodeArgs("/tmp/src.mp4", avcLadder[2], 640, 360, true, false, false), " ")
	if !strings.Contains(args, "h264_vaapi") || !strings.Contains(args, "hwupload") {
		t.Fatalf("want vaapi: %s", args)
	}
}

func TestDefaultEncoderIsX264(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "")
	if activeEncoder() != encLibx264 {
		t.Fatalf("local default should stay libx264, got %s", activeEncoder())
	}
}

func TestHWUnavailable(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_nvenc")
	if !hwUnavailable("ffmpeg: No NVENC capable devices found") {
		t.Fatal("nvenc missing should be HW")
	}
	err := wrapFFmpegErr(errors.New("ffmpeg: Cannot load nvcuda"))
	if !errors.Is(err, ErrHWUnavailable) {
		t.Fatalf("wrap: %v", err)
	}
	if hwUnavailable("ffmpeg: Invalid data found when processing input") {
		t.Fatal("corrupt media is not a device failure")
	}
}

func TestHevcHLSCodec(t *testing.T) {
	if hevcHLSCodec(2160) != "hvc1.1.6.L153.B0" {
		t.Fatalf("2160: %s", hevcHLSCodec(2160))
	}
	if hevcHLSCodec(1080) != "hvc1.1.6.L123.B0" {
		t.Fatalf("1080: %s", hevcHLSCodec(1080))
	}
	if hevcHLSCodec(720) != "hvc1.1.6.L93.B0" {
		t.Fatalf("720: %s", hevcHLSCodec(720))
	}
	got := streamCODECS(store.Rendition{Codec: "hvc1", Height: 1440})
	if got != "hvc1.1.6.L150.B0,mp4a.40.2" {
		t.Fatalf("1440 codecs: %s", got)
	}
}

func TestShouldCopyAudio(t *testing.T) {
	ok := probe{HasAudio: true, AudioCodec: "aac", AudioChannels: 2, AudioBitrateKbps: 96}
	if !shouldCopyAudio(ok, 128) {
		t.Fatal("96k stereo aac should copy into 128k target")
	}
	if shouldCopyAudio(ok, 48) {
		t.Fatal("must not copy into a lower target")
	}
	unknown := probe{HasAudio: true, AudioCodec: "aac", AudioChannels: 2, AudioBitrateKbps: 0}
	if shouldCopyAudio(unknown, 128) {
		t.Fatal("unknown bitrate must transcode")
	}
	if shouldCopyAudio(probe{HasAudio: true, AudioCodec: "aac", AudioChannels: 6, AudioBitrateKbps: 64}, 128) {
		t.Fatal("5.1 must transcode")
	}
}
