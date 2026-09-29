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

type probe struct {
	Width, Height     int
	DurationMs        int
	HasAudio          bool
	HasSubs           bool
	AudioCodec        string
	AudioChannels     int
	AudioBitrateKbps  int
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

	rungs := pickRungs(info.Width, info.Height)
	var renditions []store.Rendition
	enc := activeEncoder()
	preset := encodePreset(enc)
	labels := make([]string, len(rungs))
	for i, r := range rungs {
		labels[i] = r.Label()
	}
	writeProgress(outDir, labels, 0, 0, rungs[0])
	slog.Info("transcode start",
		"id", video.ID, "durationMs", info.DurationMs, "size", fmt.Sprintf("%dx%d", info.Width, info.Height),
		"rungs", labels, "encoder", enc, "preset", preset)

	hevcSkipped := false
	for i, rung := range rungs {
		if hevcSkipped && rung.Family == familyHEVC {
			continue
		}
		rel := filepath.ToSlash(filepath.Join(outPrefix, rung.DirName(), strconv.Itoa(rung.Height)))
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
			Codec:        rung.Codec,
		}
		if rungComplete(abs, info.DurationMs) {
			slog.Info("transcode skip rung", "id", video.ID, "label", rung.Label())
			writeProgress(outDir, labels, i, 1, rung)
			renditions = append(renditions, item)
			continue
		}
		writeProgress(outDir, labels, i, 0, rung)
		slog.Info("transcode rung", "id", video.ID, "label", rung.Label(), "encoder", enc)
		if err := encodeVideoRung(ctx, src, info, rung, abs, playlist, func(ratio float64) {
			writeProgress(outDir, labels, i, ratio, rung)
		}); err != nil {
			if wrapped := wrapFFmpegErr(err); errors.Is(wrapped, ErrHWUnavailable) {
				return wrapped
			}
			if rung.Family == familyHEVC && skipHEVC(err.Error()) {
				slog.Warn("transcode skip remaining hevc", "id", video.ID, "err", err)
				hevcSkipped = true
				continue
			}
			if rung.Family == familyHEVC {
				slog.Warn("transcode skip hevc rung", "id", video.ID, "label", rung.Label(), "err", err)
				continue
			}
			return fail(st, video.ID, err.Error())
		}
		writeProgress(outDir, labels, i, 1, rung)
		renditions = append(renditions, item)
	}
	if len(renditions) == 0 {
		return fail(st, video.ID, "no renditions")
	}

	if err := writeFamilyMasters(outDir, renditions, info); err != nil {
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
			copyA := shouldCopyAudio(info, 128)
			args := audioHLSArgs(src, filepath.Join(aAbs, "index.m3u8"), filepath.Join(aAbs, "seg_%03d.ts"), 128, copyA)
			if err := runFFmpeg(ctx, args); err != nil && copyA {
				args = audioHLSArgs(src, filepath.Join(aAbs, "index.m3u8"), filepath.Join(aAbs, "seg_%03d.ts"), 128, false)
				err = runFFmpeg(ctx, args)
			}
			if err != nil {
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

func encodeVideoRung(ctx context.Context, src string, info probe, rung ladderRung, abs, playlist string, onRatio func(float64)) error {
	seg := filepath.Join(abs, "seg_%03d.ts")
	copyA := shouldCopyAudio(info, rung.AudioK)
	// NVENC encode from system-memory frames. CUDA/NVDEC decode exhausts
	// decoder surfaces on GTX 1050-class cards (No decoder surfaces left).
	cudaTries := []bool{false}
	var last error
	for _, cuda := range cudaTries {
		for {
			args := videoEncodeArgs(src, rung, info.Width, info.Height, info.HasAudio, copyA, cuda)
			args = append(args, hlsTail(seg, playlist)...)
			last = runFFmpegProgress(ctx, args, info.DurationMs, onRatio)
			if last == nil {
				return nil
			}
			if copyA {
				copyA = false
				continue
			}
			break
		}
		if last == nil {
			return nil
		}
		if cuda && cudaSoftFail(last.Error()) {
			slog.Warn("transcode cuda fallback", "label", rung.Label(), "err", last)
			copyA = shouldCopyAudio(info, rung.AudioK)
			continue
		}
		return last
	}
	return last
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

func writeFamilyMasters(outDir string, renditions []store.Rendition, info probe) error {
	avc := filterCodec(renditions, "avc1")
	hevc := filterCodec(renditions, "hvc1")
	if len(avc) > 0 {
		if err := writeMaster(filepath.Join(outDir, "avc", "master.m3u8"), avc, info); err != nil {
			return err
		}
		if err := writeMasterPrefixed(filepath.Join(outDir, "master.m3u8"), avc, info, "avc"); err != nil {
			return err
		}
	}
	if len(hevc) > 0 {
		if err := writeMaster(filepath.Join(outDir, "hevc", "master.m3u8"), hevc, info); err != nil {
			return err
		}
	}
	return nil
}

func filterCodec(rends []store.Rendition, codec string) []store.Rendition {
	var out []store.Rendition
	for _, r := range rends {
		if r.Codec == codec {
			out = append(out, r)
		}
	}
	return out
}

func writeMaster(path string, renditions []store.Rendition, info probe) error {
	return writeMasterPrefixed(path, renditions, info, "")
}

func writeMasterPrefixed(path string, renditions []store.Rendition, info probe, prefix string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range renditions {
		w, h := outputSize(info.Width, info.Height, r.Height)
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%dx%d,CODECS=%q\n", r.BandwidthBps, w, h, streamCODECS(r))
		rel := fmt.Sprintf("%d/index.m3u8", r.Height)
		if prefix != "" {
			rel = prefix + "/" + rel
		}
		b.WriteString(rel + "\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func outputSize(srcW, srcH, rungH int) (w, h int) {
	if srcW <= 0 || srcH <= 0 {
		return rungH, rungH
	}
	if isPortrait(srcW, srcH) {
		w = rungH
		h = int(math.Round(float64(rungH) * float64(srcH) / float64(srcW)))
	} else {
		h = rungH
		w = int(math.Round(float64(rungH) * float64(srcW) / float64(srcH)))
	}
	if w%2 != 0 {
		w++
	}
	if h%2 != 0 {
		h++
	}
	return w, h
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
			CodecName string `json:"codec_name"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
			Channels  int    `json:"channels"`
			BitRate   string `json:"bit_rate"`
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
			if p.AudioCodec == "" {
				p.AudioCodec = s.CodecName
				p.AudioChannels = s.Channels
				if br, err := strconv.Atoi(s.BitRate); err == nil && br > 0 {
					p.AudioBitrateKbps = br / 1000
				}
			}
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
