package transcode

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrHWUnavailable means this worker's encoder/device cannot run. The job
// should go back on the queue for a lower-rank worker; the video is not failed.
var ErrHWUnavailable = errors.New("hardware encoder unavailable")

const (
	encLibx264 = "libx264"
	encNVENC   = "h264_nvenc"
	encQSV     = "h264_qsv"
	encVAAPI   = "h264_vaapi"
)

func activeEncoder() string {
	switch strings.ToLower(strings.TrimSpace(getenv("TRANSCODE_ENCODER", encLibx264))) {
	case "h264_nvenc", "nvenc":
		return encNVENC
	case "h264_qsv", "qsv":
		return encQSV
	case "h264_vaapi", "vaapi":
		return encVAAPI
	default:
		return encLibx264
	}
}

func maxEncodeHeight() int {
	n, err := strconv.Atoi(getenv("TRANSCODE_MAX_HEIGHT", "1080"))
	if err != nil || n <= 0 {
		return 1080
	}
	return n
}

func encodePreset(enc string) string {
	p := getenv("TRANSCODE_PRESET", "")
	if p != "" {
		if enc == encNVENC && (p == "veryfast" || p == "ultrafast" || p == "superfast" || p == "p4" || p == "p5") {
			return "hq"
		}
		return p
	}
	switch enc {
	case encNVENC:
		return "hq"
	case encQSV:
		return "medium"
	default:
		return "veryfast"
	}
}

func videoEncodeArgs(src string, rung ladderRung) []string {
	enc := activeEncoder()
	preset := encodePreset(enc)
	switch enc {
	case encNVENC:
		return nvencArgs(src, rung, preset)
	case encQSV:
		return qsvArgs(src, rung, preset)
	case encVAAPI:
		return vaapiArgs(src, rung)
	default:
		return x264Args(src, rung, preset)
	}
}

func x264Args(src string, rung ladderRung, preset string) []string {
	return append([]string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale=-2:%d", rung.Height),
		"-c:v", encLibx264, "-preset", preset,
	}, rateControl(rung, true)...)
}

func nvencArgs(src string, rung ladderRung, preset string) []string {
	// CPU decode + CPU scale: GTX 960 只有 2GB，CUDA 解码容易 OOM。
	return append([]string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale=-2:%d", rung.Height),
		"-c:v", encNVENC, "-preset", preset, "-rc", "vbr",
		"-b:v", fmt.Sprintf("%dk", rung.VideoK),
		"-maxrate", fmt.Sprintf("%dk", rung.VideoK*2),
		"-bufsize", fmt.Sprintf("%dk", rung.VideoK*3),
		"-g", "48", "-bf", "0", "-pix_fmt", "yuv420p",
	})
}

func qsvArgs(src string, rung ladderRung, preset string) []string {
	return append([]string{
		"-y", "-i", src,
		"-vf", fmt.Sprintf("scale=-2:%d", rung.Height),
		"-c:v", encQSV, "-preset", preset, "-look_ahead", "0",
	}, rateControl(rung, false)...)
}

func vaapiArgs(src string, rung ladderRung) []string {
	dev := getenv("VAAPI_DEVICE", "/dev/dri/renderD128")
	return []string{
		"-y", "-vaapi_device", dev, "-i", src,
		"-vf", fmt.Sprintf("scale=-2:%d,format=nv12,hwupload", rung.Height),
		"-c:v", encVAAPI,
		"-b:v", fmt.Sprintf("%dk", rung.VideoK),
		"-maxrate", fmt.Sprintf("%dk", rung.VideoK*2),
		"-bufsize", fmt.Sprintf("%dk", rung.VideoK*3),
		"-g", "48", "-bf", "0",
	}
}

func rateControl(rung ladderRung, scThreshold bool) []string {
	args := []string{
		"-b:v", fmt.Sprintf("%dk", rung.VideoK),
		"-maxrate", fmt.Sprintf("%dk", rung.VideoK*2),
		"-bufsize", fmt.Sprintf("%dk", rung.VideoK*3),
		"-g", "48", "-keyint_min", "48", "-bf", "0", "-pix_fmt", "yuv420p",
	}
	if scThreshold {
		args = append(args, "-sc_threshold", "0")
	}
	return args
}

func audioHLSArgs(src, playlist, segPattern string, bitrateK int) []string {
	return []string{
		"-y", "-i", src, "-vn", "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", bitrateK), "-ac", "2",
		"-hls_time", "4", "-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		playlist,
	}
}

func hlsTail(segPattern, playlist string) []string {
	return []string{
		"-hls_time", "4", "-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		playlist,
	}
}

func hwUnavailable(msg string) bool {
	s := strings.ToLower(msg)
	needles := []string{
		"no nvenc capable",
		"cannot load nvcuda",
		"cannot load libcuda",
		"cannot load libnvcuvid",
		"cuda_error",
		"openencodesession",
		"unknown encoder",
		"encoder not found",
		"not found for encoder",
		"failed to initialize encoder",
		"failed to create a qsv device",
		"failed to create a vaapi device",
		"error creating a qsv device",
		"no device available",
		"device creation failed",
		"function not implemented",
		"driver does not support",
		"libva error",
		"vainitialize failed",
		"failed to initialise vaapi",
		"invalid argument found for encoder",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	if strings.Contains(s, "error while opening encoder") && activeEncoder() != encLibx264 {
		return true
	}
	return false
}

func wrapFFmpegErr(err error) error {
	if err == nil {
		return nil
	}
	if hwUnavailable(err.Error()) {
		return fmt.Errorf("%w: %s", ErrHWUnavailable, err.Error())
	}
	return err
}
