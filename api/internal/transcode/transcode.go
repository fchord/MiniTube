package transcode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

type ladderRung struct {
	Height, VideoK, AudioK, Bandwidth int
}

var ladder = []ladderRung{
	{360, 800, 96, 900_000},
	{720, 2500, 128, 2_800_000},
	{1080, 5000, 160, 5_500_000},
}

type probe struct {
	Width, Height int
	DurationMs    int
	HasAudio      bool
	HasSubs       bool
}

func persistCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func fail(st *store.Postgres, id string, msg string) error {
	ctx, cancel := persistCtx()
	defer cancel()
	return st.SaveTranscodeFailure(ctx, id, msg)
}

func Process(ctx context.Context, st *store.Postgres, fs *storage.Local, video store.Video) error {
	hb, stopHB := context.WithCancel(context.Background())
	defer stopHB()
	go func() {
		t := time.NewTicker(2 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-hb.Done():
				return
			case <-t.C:
				db, cancel := persistCtx()
				_ = st.TouchTranscodeClaim(db, video.ID)
				cancel()
			}
		}
	}()

	if video.SourceObjectKey == nil {
		return fail(st, video.ID, "missing source")
	}
	src, err := fs.Abs(*video.SourceObjectKey)
	if err != nil {
		return fail(st, video.ID, "source path")
	}
	info, err := ffprobe(ctx, src)
	if err != nil {
		return fail(st, video.ID, err.Error())
	}
	outPrefix := filepath.ToSlash(filepath.Join("hls", video.ID))
	outDir, err := fs.Abs(outPrefix)
	if err != nil {
		return fail(st, video.ID, err.Error())
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fail(st, video.ID, err.Error())
	}

	rungs := pickRungs(info.Height)
	var renditions []store.Rendition
	enc := activeEncoder()
	preset := encodePreset(enc)
	heights := make([]int, len(rungs))
	for i, r := range rungs {
		heights[i] = r.Height
	}
	writeProgress(outDir, heights, 0, 0)
	slog.Info("transcode start",
		"id", video.ID, "durationMs", info.DurationMs, "size", fmt.Sprintf("%dx%d", info.Width, info.Height),
		"rungs", heights, "encoder", enc, "preset", preset)
	for i, rung := range rungs {
		rel := filepath.ToSlash(filepath.Join(outPrefix, strconv.Itoa(rung.Height)))
		abs, err := fs.Abs(rel)
		if err != nil {
			return fail(st, video.ID, err.Error())
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return fail(st, video.ID, err.Error())
		}
		playlist := filepath.Join(abs, "index.m3u8")
		item := store.Rendition{
			Height:       rung.Height,
			BandwidthBps: rung.Bandwidth,
			PlaylistKey:  rel + "/index.m3u8",
			Codec:        "avc1",
		}
		if rungComplete(abs, info.DurationMs) {
			slog.Info("transcode skip rung", "id", video.ID, "height", rung.Height)
			writeProgress(outDir, heights, i, 1)
			renditions = append(renditions, item)
			continue
		}
		args := videoEncodeArgs(src, rung)
		if info.HasAudio {
			args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", rung.AudioK), "-ac", "2")
		} else {
			args = append(args, "-an")
		}
		args = append(args, hlsTail(filepath.Join(abs, "seg_%03d.ts"), playlist)...)
		slog.Info("transcode rung", "id", video.ID, "height", rung.Height, "encoder", enc)
		writeProgress(outDir, heights, i, 0)
		if err := runFFmpegProgress(ctx, args, info.DurationMs, func(ratio float64) {
			writeProgress(outDir, heights, i, ratio)
		}); err != nil {
			if wrapped := wrapFFmpegErr(err); errors.Is(wrapped, ErrHWUnavailable) {
				return wrapped
			}
			return fail(st, video.ID, err.Error())
		}
		writeProgress(outDir, heights, i, 1)
		renditions = append(renditions, item)
	}

	if err := writeMaster(filepath.Join(outDir, "master.m3u8"), renditions, info); err != nil {
		return fail(st, video.ID, err.Error())
	}

	var audio []store.AudioTrack
	if info.HasAudio {
		aRel := outPrefix + "/audio"
		aAbs, err := fs.Abs(aRel)
		if err != nil {
			return fail(st, video.ID, err.Error())
		}
		if err := os.MkdirAll(aAbs, 0o755); err != nil {
			return fail(st, video.ID, err.Error())
		}
		if !rungComplete(aAbs, info.DurationMs) {
			args := audioHLSArgs(src, filepath.Join(aAbs, "index.m3u8"), filepath.Join(aAbs, "seg_%03d.ts"), 128)
			if err := runFFmpeg(ctx, args); err != nil {
				return fail(st, video.ID, err.Error())
			}
		}
		audio = append(audio, store.AudioTrack{Language: "und", Label: "Default", IsDefault: true, PlaylistKey: aRel + "/index.m3u8"})
	}

	var subs []store.SubtitleTrack
	if info.HasSubs {
		vttRel := outPrefix + "/sub.vtt"
		vttAbs, err := fs.Abs(vttRel)
		if err == nil {
			if err := runFFmpeg(ctx, []string{"-y", "-i", src, "-map", "0:s:0", vttAbs}); err == nil {
				subs = append(subs, store.SubtitleTrack{Language: "und", Label: "Default", IsDefault: true, VTTKey: vttRel})
			}
		}
	}

	thumbRel := outPrefix + "/thumb.jpg"
	thumbAbs, _ := fs.Abs(thumbRel)
	ss := "1"
	if info.DurationMs > 3000 {
		ss = fmt.Sprintf("%.2f", float64(info.DurationMs)/1000.0*0.1)
	}
	_ = runFFmpeg(ctx, []string{"-y", "-ss", ss, "-i", src, "-frames:v", "1", "-q:v", "3", thumbAbs})
	if _, err := os.Stat(thumbAbs); err != nil {
		thumbRel = ""
	}
	thumbURL := ""
	if thumbRel != "" {
		thumbURL = fs.URL(thumbRel)
	}

	ar := 0.0
	if info.Height > 0 {
		ar = float64(info.Width) / float64(info.Height)
	}
	db, cancel := persistCtx()
	err = st.SaveTranscodeSuccess(db, video.ID, store.TranscodeResult{
		DurationMs:   info.DurationMs,
		Width:        info.Width,
		Height:       info.Height,
		AspectRatio:  ar,
		ThumbnailKey: thumbURL,
		Renditions:   renditions,
		Audio:        audio,
		Subtitles:    subs,
	})
	cancel()
	if err == nil {
		fs.RemovePrefix("tmp/" + video.ID)
		slog.Info("transcode ready", "id", video.ID)
	}
	return err
}

func rungComplete(dir string, durationMs int) bool {
	st, err := os.Stat(filepath.Join(dir, "index.m3u8"))
	if err != nil || st.Size() < 16 {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "seg_*.ts"))
	need := durationMs / 4000
	if need < 1 {
		need = 1
	}
	if need > 2 {
		need -= 2
	}
	return len(matches) >= need
}

func pickRungs(srcH int) []ladderRung {
	return pickRungsCapped(srcH, maxEncodeHeight())
}

func pickRungsCapped(srcH, maxH int) []ladderRung {
	var out []ladderRung
	for _, r := range ladder {
		if srcH >= r.Height && r.Height <= maxH {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		h := srcH
		if h%2 != 0 {
			h--
		}
		if h < 16 {
			h = 16
		}
		out = append(out, ladderRung{Height: h, VideoK: 400, AudioK: 96, Bandwidth: 500_000})
	}
	return out
}

func writeMaster(path string, renditions []store.Rendition, info probe) error {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range renditions {
		w := int(math.Round(float64(r.Height) * float64(info.Width) / math.Max(float64(info.Height), 1)))
		if w%2 != 0 {
			w++
		}
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=\"avc1.4d401f,mp4a.40.2\"\n", r.BandwidthBps, w, r.Height)
		fmt.Fprintf(&b, "%d/index.m3u8\n", r.Height)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func ffprobe(ctx context.Context, src string) (probe, error) {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", src)
	out, err := cmd.Output()
	if err != nil {
		return probe{}, fmt.Errorf("ffprobe: %w", err)
	}
	var parsed struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Duration  string `json:"duration"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return probe{}, err
	}
	p := probe{}
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			if p.Width == 0 {
				p.Width, p.Height = s.Width, s.Height
			}
		case "audio":
			p.HasAudio = true
		case "subtitle":
			p.HasSubs = true
		}
	}
	if d, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
		p.DurationMs = int(d * 1000)
	}
	if p.Width == 0 || p.Height == 0 {
		return probe{}, fmt.Errorf("no video stream")
	}
	return p, nil
}

func runFFmpeg(ctx context.Context, args []string) error {
	return runFFmpegProgress(ctx, args, 0, nil)
}

func runFFmpegProgress(ctx context.Context, args []string, durationMs int, onRatio func(float64)) error {
	if onRatio != nil {
		if len(args) > 0 && args[0] == "-y" {
			args = append([]string{"-y", "-nostats", "-progress", "pipe:1"}, args[1:]...)
		} else {
			args = append([]string{"-nostats", "-progress", "pipe:1"}, args...)
		}
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if onRatio != nil {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		go consumeProgress(stdout, durationMs, onRatio)
		err = cmd.Wait()
		if err != nil {
			msg := stderr.String()
			if len(msg) > 800 {
				msg = msg[len(msg)-800:]
			}
			return fmt.Errorf("ffmpeg: %s", msg)
		}
		return nil
	}
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if len(msg) > 800 {
			msg = msg[len(msg)-800:]
		}
		return fmt.Errorf("ffmpeg: %s", msg)
	}
	return nil
}

func consumeProgress(r io.Reader, durationMs int, onRatio func(float64)) {
	if onRatio == nil || durationMs <= 0 {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	last := time.Time{}
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "out_time=") {
			continue
		}
		d, err := parseOutTime(strings.TrimPrefix(line, "out_time="))
		if err != nil {
			continue
		}
		if time.Since(last) < 400*time.Millisecond {
			continue
		}
		last = time.Now()
		ratio := float64(d) / float64(time.Duration(durationMs)*time.Millisecond)
		if ratio > 1 {
			ratio = 1
		}
		if ratio < 0 {
			ratio = 0
		}
		onRatio(ratio)
	}
}

func parseOutTime(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("bad out_time")
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	sec, err3 := strconv.ParseFloat(parts[2], 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, fmt.Errorf("bad out_time")
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(sec*float64(time.Second)), nil
}

func ExtractJPEG(ctx context.Context, src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	args := []string{"-y"}
	if strings.HasSuffix(strings.ToLower(src), ".m3u8") {
		args = append(args, "-allowed_extensions", "ALL")
	}
	args = append(args, "-i", src, "-frames:v", "1", "-q:v", "3", dst)
	if err := runFFmpeg(ctx, args); err != nil {
		return err
	}
	_, err := os.Stat(dst)
	return err
}

func getenv(k, fb string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fb
}

type Worker struct {
	ID      string
	Rank    int
	Encoder string
}

func RunLoop(ctx context.Context, st *store.Postgres, fs *storage.Local, every time.Duration, w *Worker) {
	workerID := ""
	if w != nil && w.Rank >= 1 && w.ID != "" {
		workerID = w.ID
		if err := st.UpsertTranscodeWorker(ctx, w.ID, w.Rank, w.Encoder); err != nil {
			slog.Error("transcode worker register", "id", w.ID, "err", err)
		} else {
			slog.Info("transcode worker registered", "id", w.ID, "rank", w.Rank, "encoder", w.Encoder)
		}
		go heartbeatWorker(ctx, st, w.ID)
		defer func() { _ = st.SetTranscodeWorkerIdle(context.Background(), w.ID) }()
	}

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		v, err := st.ClaimTranscodeJob(ctx, workerID)
		if err == nil && v.ID != "" {
			err := Process(ctx, st, fs, v)
			if workerID != "" {
				if errors.Is(err, ErrHWUnavailable) {
					_ = st.ReleaseTranscodeClaim(context.Background(), v.ID)
					_ = st.MarkTranscodeWorkerUnhealthy(context.Background(), workerID)
					slog.Error("transcode hardware unavailable; job requeued", "id", v.ID, "worker", workerID, "err", err)
				} else {
					_ = st.SetTranscodeWorkerIdle(context.Background(), workerID)
				}
			}
			if err != nil {
				slog.Error("transcode failed", "id", v.ID, "err", err)
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func heartbeatWorker(ctx context.Context, st *store.Postgres, id string) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = st.TouchTranscodeWorker(context.Background(), id)
		}
	}
}
