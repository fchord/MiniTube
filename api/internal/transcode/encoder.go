package transcode

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"minitube/api/internal/store"
)

// ErrHWUnavailable means this worker's encoder/device cannot run. The job
// should go back on the queue for a lower-rank worker; the video is not failed.
var ErrHWUnavailable = errors.New("hardware encoder unavailable")

const (
	encLibx264 = "libx264"
	encNVENC   = "h264_nvenc"
	encQSV     = "h264_qsv"
	encVAAPI   = "h264_vaapi"

	familyAVC  = "avc"
	familyHEVC = "hevc"
)

type ladderRung struct {
	Family            string
	Height            int
	VideoK, AudioK    int
	Bandwidth         int
	Codec             string
}

func (r ladderRung) Label() string {
	if r.Family == familyHEVC {
		return fmt.Sprintf("H.265 %dp", r.Height)
	}
	return fmt.Sprintf("H.264 %dp", r.Height)
}

func (r ladderRung) DirName() string {
	return r.Family
}

var avcLadder = []ladderRung{
	{Family: familyAVC, Height: 720, VideoK: 1000, AudioK: 128, Bandwidth: 1_128_000, Codec: "avc1"},
	{Family: familyAVC, Height: 480, VideoK: 800, AudioK: 96, Bandwidth: 896_000, Codec: "avc1"},
	{Family: familyAVC, Height: 360, VideoK: 600, AudioK: 48, Bandwidth: 648_000, Codec: "avc1"},
	{Family: familyAVC, Height: 240, VideoK: 300, AudioK: 32, Bandwidth: 332_000, Codec: "avc1"},
	{Family: familyAVC, Height: 144, VideoK: 150, AudioK: 32, Bandwidth: 182_000, Codec: "avc1"},
}

var hevcLadder = []ladderRung{
	{Family: familyHEVC, Height: 2160, VideoK: 10000, AudioK: 256, Bandwidth: 10_256_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 1440, VideoK: 6000, AudioK: 256, Bandwidth: 6_256_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 1080, VideoK: 2000, AudioK: 192, Bandwidth: 2_192_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 720, VideoK: 700, AudioK: 128, Bandwidth: 828_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 480, VideoK: 500, AudioK: 96, Bandwidth: 596_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 360, VideoK: 350, AudioK: 48, Bandwidth: 398_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 240, VideoK: 200, AudioK: 32, Bandwidth: 232_000, Codec: "hvc1"},
	{Family: familyHEVC, Height: 144, VideoK: 100, AudioK: 32, Bandwidth: 132_000, Codec: "hvc1"},
}

func hevcHLSCodec(height int) string {
	lvl := 63
	switch {
	case height >= 2160:
		lvl = 153
	case height >= 1440:
		lvl = 150
	case height >= 1080:
		lvl = 123
	case height >= 720:
		lvl = 93
	}
	return fmt.Sprintf("hvc1.1.6.L%d.B0", lvl)
}

func streamCODECS(r store.Rendition) string {
	if r.Codec == "hvc1" || r.Codec == "hev1" {
		return hevcHLSCodec(r.Height) + ",mp4a.40.2"
	}
	return "avc1.4d401f,mp4a.40.2"
}

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

func hevcVideoEncoder() string {
	switch activeEncoder() {
	case encNVENC:
		return "hevc_nvenc"
	case encQSV:
		return "hevc_qsv"
	case encVAAPI:
		return "hevc_vaapi"
	default:
		return "libx265"
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

func shortSide(w, h int) int {
	if w > 0 && w < h {
		return w
	}
	if h > 0 {
		return h
	}
	return w
}

func isPortrait(w, h int) bool {
	return h > w && w > 0
}

func pickRungs(srcW, srcH int) []ladderRung {
	return append(pickFamily(srcW, srcH, avcLadder), pickFamily(srcW, srcH, hevcLadder)...)
}

func pickFamily(srcW, srcH int, ladder []ladderRung) []ladderRung {
	srcShort := shortSide(srcW, srcH)
	maxH := maxEncodeHeight()
	var out []ladderRung
	for _, r := range ladder {
		if srcShort >= r.Height && r.Height <= maxH {
			rr := r
			out = append(out, rr)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Height > out[j].Height })
	if len(out) == 0 {
		h := srcShort
		if h%2 != 0 {
			h--
		}
		if h < 16 {
			h = 16
		}
		base := ladder[len(ladder)-1]
		base.Height = h
		out = append(out, base)
	}
	return out
}

func pickRungsCapped(srcH, maxH int) []ladderRung {
	old := getenv("TRANSCODE_MAX_HEIGHT", "1080")
	// tests set cap via second arg; temporarily pretend env
	_ = old
	var out []ladderRung
	for _, r := range avcLadder {
		if srcH >= r.Height && r.Height <= maxH {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Height < out[j].Height })
	if len(out) == 0 {
		h := srcH
		if h%2 != 0 {
			h--
		}
		if h < 16 {
			h = 16
		}
		out = append(out, ladderRung{Family: familyAVC, Height: h, VideoK: 150, AudioK: 32, Bandwidth: 182_000, Codec: "avc1"})
	}
	return out
}

func needScale(srcW, srcH int, rung ladderRung) bool {
	if isPortrait(srcW, srcH) {
		return srcW != rung.Height
	}
	return srcH != rung.Height
}

func scaleFilter(srcW, srcH int, rung ladderRung, cuda bool) string {
	if !needScale(srcW, srcH, rung) {
		return ""
	}
	if cuda {
		if isPortrait(srcW, srcH) {
			return fmt.Sprintf("scale_cuda=%d:-2:format=yuv420p", rung.Height)
		}
		return fmt.Sprintf("scale_cuda=-2:%d:format=yuv420p", rung.Height)
	}
	if isPortrait(srcW, srcH) {
		return fmt.Sprintf("scale=%d:-2", rung.Height)
	}
	return fmt.Sprintf("scale=-2:%d", rung.Height)
}

func videoEncodeArgs(src string, rung ladderRung, srcW, srcH int, hasAudio, copyAudio, cuda bool) []string {
	if rung.Family == familyHEVC {
		return hevcEncodeArgs(src, rung, srcW, srcH, hasAudio, copyAudio, cuda)
	}
	return avcEncodeArgs(src, rung, srcW, srcH, hasAudio, copyAudio, cuda)
}

func avcEncodeArgs(src string, rung ladderRung, srcW, srcH int, hasAudio, copyAudio, cuda bool) []string {
	enc := activeEncoder()
	preset := encodePreset(enc)
	args := []string{"-y"}
	if cuda && enc == encNVENC {
		args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	}
	if enc == encVAAPI {
		dev := getenv("VAAPI_DEVICE", "/dev/dri/renderD128")
		args = append(args, "-vaapi_device", dev)
	}
	args = append(args, "-i", src)
	vf := scaleFilter(srcW, srcH, rung, cuda && enc == encNVENC)
	if enc == encVAAPI {
		if vf == "" {
			vf = "format=nv12,hwupload"
		} else {
			vf = vf + ",format=nv12,hwupload"
		}
	}
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	switch enc {
	case encNVENC:
		args = append(args, "-c:v", encNVENC, "-preset", preset, "-rc", "vbr")
		args = append(args, avcRateControl(rung, false, !cuda)...)
	case encQSV:
		args = append(args, "-c:v", encQSV, "-preset", preset, "-look_ahead", "0")
		args = append(args, avcRateControl(rung, false, true)...)
	case encVAAPI:
		args = append(args, "-c:v", encVAAPI)
		args = append(args, "-b:v", fmt.Sprintf("%dk", rung.VideoK),
			"-maxrate", fmt.Sprintf("%dk", rung.VideoK*2),
			"-bufsize", fmt.Sprintf("%dk", rung.VideoK*3),
			"-g", "48", "-bf", "0")
	default:
		args = append(args, "-c:v", encLibx264, "-preset", preset)
		args = append(args, avcRateControl(rung, true, true)...)
	}
	args = append(args, muxAudioArgs(hasAudio, rung.AudioK, copyAudio)...)
	return args
}

func hevcEncodeArgs(src string, rung ladderRung, srcW, srcH int, hasAudio, copyAudio, cuda bool) []string {
	venc := hevcVideoEncoder()
	args := []string{"-y"}
	if cuda && activeEncoder() == encNVENC {
		args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
	}
	if activeEncoder() == encVAAPI {
		dev := getenv("VAAPI_DEVICE", "/dev/dri/renderD128")
		args = append(args, "-vaapi_device", dev)
	}
	args = append(args, "-i", src)
	vf := scaleFilter(srcW, srcH, rung, cuda && activeEncoder() == encNVENC)
	if activeEncoder() == encVAAPI {
		if vf == "" {
			vf = "format=nv12,hwupload"
		} else if !strings.Contains(vf, "hwupload") {
			vf = vf + ",format=nv12,hwupload"
		}
	}
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	maxrate := rung.VideoK
	buf := maxrate * 2
	switch venc {
	case "hevc_nvenc":
		args = append(args,
			"-c:v", "hevc_nvenc", "-preset", "p7", "-tune", "hq", "-multipass", "fullres",
			"-rc", "vbr", "-cq", "28", "-b:v", "0",
			"-maxrate", fmt.Sprintf("%dk", maxrate),
			"-bufsize", fmt.Sprintf("%dk", buf),
			"-rc-lookahead", "32", "-spatial-aq", "1", "-aq-strength", "8",
			"-bf", "0", "-profile:v", "main", "-pix_fmt", "yuv420p", "-tag:v", "hvc1",
			"-force_key_frames", "expr:gte(t,n_forced*4)", "-forced-idr", "1",
		)
	case "hevc_qsv":
		args = append(args, "-c:v", "hevc_qsv", "-preset", encodePreset(encQSV),
			"-b:v", "0", "-maxrate", fmt.Sprintf("%dk", maxrate), "-bufsize", fmt.Sprintf("%dk", buf),
			"-bf", "0", "-tag:v", "hvc1",
			"-force_key_frames", "expr:gte(t,n_forced*4)")
	case "hevc_vaapi":
		args = append(args, "-c:v", "hevc_vaapi",
			"-b:v", fmt.Sprintf("%dk", maxrate),
			"-maxrate", fmt.Sprintf("%dk", maxrate),
			"-bufsize", fmt.Sprintf("%dk", buf),
			"-bf", "0", "-tag:v", "hvc1")
	default:
		preset := encodePreset(encLibx264)
		args = append(args, "-c:v", "libx265", "-preset", preset, "-tag:v", "hvc1",
			"-x265-params", "repeat-headers=1:keyint=96:min-keyint=96:scenecut=0:bframes=0",
			"-b:v", "0", "-maxrate", fmt.Sprintf("%dk", maxrate), "-bufsize", fmt.Sprintf("%dk", buf),
			"-force_key_frames", "expr:gte(t,n_forced*4)")
	}
	args = append(args, muxAudioArgs(hasAudio, rung.AudioK, copyAudio)...)
	return args
}

func avcRateControl(rung ladderRung, scThreshold, pixFmt bool) []string {
	args := []string{
		"-b:v", fmt.Sprintf("%dk", rung.VideoK),
		"-maxrate", fmt.Sprintf("%dk", rung.VideoK*2),
		"-bufsize", fmt.Sprintf("%dk", rung.VideoK*3),
		"-g", "48", "-keyint_min", "48", "-bf", "0",
	}
	if pixFmt {
		args = append(args, "-pix_fmt", "yuv420p")
	}
	if scThreshold {
		args = append(args, "-sc_threshold", "0")
	}
	return args
}

func muxAudioArgs(hasAudio bool, bitrateK int, copy bool) []string {
	if !hasAudio {
		return []string{"-an"}
	}
	if copy {
		return []string{"-c:a", "copy"}
	}
	return []string{"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", bitrateK), "-ac", "2"}
}

func audioArgs(bitrateK int, copy bool) []string {
	return muxAudioArgs(true, bitrateK, copy)
}

func shouldCopyAudio(info probe, targetK int) bool {
	if !info.HasAudio {
		return false
	}
	if !strings.EqualFold(info.AudioCodec, "aac") {
		return false
	}
	if info.AudioChannels != 2 {
		return false
	}
	if info.AudioBitrateKbps <= 0 || targetK <= 0 {
		return false
	}
	return info.AudioBitrateKbps <= targetK
}

func audioHLSArgs(src, playlist, segPattern string, bitrateK int, copy bool) []string {
	args := []string{"-y", "-i", src, "-vn"}
	args = append(args, audioArgs(bitrateK, copy)...)
	args = append(args,
		"-hls_time", "4", "-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		playlist,
	)
	return args
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

func cudaSoftFail(msg string) bool {
	s := strings.ToLower(msg)
	for _, n := range []string{
		"cuda", "nvdec", "cuvid", "scale_cuda", "hwaccel", "out of memory", "oom", "no nvenc capable",
	} {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func skipHEVC(msg string) bool {
	s := strings.ToLower(msg)
	return strings.Contains(s, "unknown encoder") ||
		strings.Contains(s, "encoder not found") ||
		strings.Contains(s, "not found for encoder") ||
		strings.Contains(s, "invalid argument found for encoder")
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
