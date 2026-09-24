package transcode

import (
	"errors"
	"strings"
	"testing"
)

func TestPickRungsCapped(t *testing.T) {
	h := pickRungsCapped(1080, 720)
	if len(h) != 2 || h[0].Height != 360 || h[1].Height != 720 {
		t.Fatalf("got %#v", h)
	}
	all := pickRungsCapped(1080, 1080)
	if len(all) != 3 || all[2].Height != 1080 {
		t.Fatalf("1080 cap should keep 1080: %#v", all)
	}
	low := pickRungsCapped(240, 1080)
	if len(low) != 1 || low[0].Height < 16 {
		t.Fatalf("short source: %#v", low)
	}
}

func TestVideoEncodeArgsNVENC(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_nvenc")
	t.Setenv("TRANSCODE_PRESET", "hq")
	args := strings.Join(videoEncodeArgs("/tmp/src.mp4", ladder[1]), " ")
	if !strings.Contains(args, "h264_nvenc") {
		t.Fatalf("want nvenc: %s", args)
	}
	if strings.Contains(args, "libx264") {
		t.Fatalf("must not use x264: %s", args)
	}
	if strings.Contains(args, "hwaccel") {
		t.Fatalf("960/1050 走 CPU 解码: %s", args)
	}
}

func TestVideoEncodeArgsQSV(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_qsv")
	args := strings.Join(videoEncodeArgs("/tmp/src.mp4", ladder[0]), " ")
	if !strings.Contains(args, "h264_qsv") {
		t.Fatalf("want qsv: %s", args)
	}
}

func TestVideoEncodeArgsVAAPI(t *testing.T) {
	t.Setenv("TRANSCODE_ENCODER", "h264_vaapi")
	args := strings.Join(videoEncodeArgs("/tmp/src.mp4", ladder[0]), " ")
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
