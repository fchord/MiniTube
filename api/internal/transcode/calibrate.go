package transcode

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const calibrateClip = 6 // seconds

func LoadOrCalibrate(ctx context.Context, path, preset string) *SpeedModel {
	fresh := defaultSpeedModel()
	fresh.Preset = preset
	fresh.GPU = detectGPU()
	if cached, err := loadSpeedModel(path); err == nil && cached.fingerprint(preset) == fresh.fingerprint(preset) {
		if cached.HeightSpeed[360] > 0 && cached.HeightSpeed[720] > 0 && cached.HeightSpeed[1080] > 0 &&
			time.Since(cached.UpdatedAt) < 14*24*time.Hour {
			cached.GPU = fresh.GPU
			setModel(cached, path)
			slog.Info("transcode speed cache hit", "path", path, "heightSpeed", cached.HeightSpeed, "audioSpeed", cached.AudioSpeed)
			return cached
		}
	}
	m := calibrate(ctx, preset)
	m.GPU = fresh.GPU
	if err := saveSpeedModel(path, m); err != nil {
		slog.Warn("transcode speed cache write", "err", err)
	}
	setModel(m, path)
	slog.Info("transcode speed calibrated", "encoder", m.Encoder, "preset", m.Preset, "cpu", m.CPU, "nproc", m.Nproc, "gpu", m.GPU, "heightSpeed", m.HeightSpeed, "audioSpeed", m.AudioSpeed)
	return m
}

func calibrate(ctx context.Context, preset string) *SpeedModel {
	m := defaultSpeedModel()
	m.Encoder = activeEncoder()
	m.Preset = preset
	m.UpdatedAt = time.Now()
	dir, err := os.MkdirTemp("", "mt-cal-")
	if err != nil {
		return m
	}
	defer os.RemoveAll(dir)
	for _, r := range ladder {
		if ctx.Err() != nil {
			return m
		}
		sp, err := measureVideo(ctx, dir, preset, r)
		if err != nil {
			slog.Warn("transcode calibrate rung", "height", r.Height, "err", err)
			continue
		}
		m.HeightSpeed[r.Height] = clampSpeed(sp)
		slog.Info("transcode calibrate rung", "height", r.Height, "speed", m.HeightSpeed[r.Height])
	}
	if ctx.Err() == nil {
		if sp, err := measureAudio(ctx, dir); err != nil {
			slog.Warn("transcode calibrate audio", "err", err)
		} else {
			m.AudioSpeed = clampSpeed(sp)
			slog.Info("transcode calibrate audio", "speed", m.AudioSpeed)
		}
	}
	return m
}

func measureVideo(ctx context.Context, dir, preset string, r ladderRung) (float64, error) {
	args := []string{
		"-y", "-hide_banner", "-nostats",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=24:duration=" + strconv.Itoa(calibrateClip),
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + strconv.Itoa(calibrateClip),
		"-t", strconv.Itoa(calibrateClip),
		"-vf", "scale=-2:" + strconv.Itoa(r.Height),
		"-c:v", activeEncoder(), "-preset", preset,
		"-b:v", strconv.Itoa(r.VideoK) + "k",
		"-maxrate", strconv.Itoa(r.VideoK*2) + "k",
		"-bufsize", strconv.Itoa(r.VideoK*3) + "k",
		"-g", "48", "-keyint_min", "48", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "96k", "-ac", "2",
		"-f", "null", "-",
	}
	return measureSpeed(ctx, dir, args)
}

func measureAudio(ctx context.Context, dir string) (float64, error) {
	args := []string{
		"-y", "-hide_banner", "-nostats",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=" + strconv.Itoa(calibrateClip),
		"-t", strconv.Itoa(calibrateClip),
		"-vn", "-c:a", "aac", "-b:a", "128k", "-ac", "2",
		"-f", "null", "-",
	}
	return measureSpeed(ctx, dir, args)
}

func measureSpeed(ctx context.Context, dir string, args []string) (float64, error) {
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	err := cmd.Run()
	wall := time.Since(start)
	if err != nil {
		return 0, err
	}
	if wall < 50*time.Millisecond {
		wall = 50 * time.Millisecond
	}
	return float64(calibrateClip) * float64(time.Second) / float64(wall), nil
}

func detectGPU() string {
	out, err := exec.Command("nvidia-smi", "-L").Output()
	if err != nil || len(bytes.TrimSpace(out)) == 0 {
		return "none (encoder " + activeEncoder() + ")"
	}
	line := strings.TrimSpace(strings.Split(string(out), "\n")[0])
	return line + " (encoder " + activeEncoder() + ")"
}
