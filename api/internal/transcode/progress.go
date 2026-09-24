package transcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

type JobProgress struct {
	Rungs   []int   `json:"rungs"`
	Index   int     `json:"index"`
	Ratio   float64 `json:"ratio"`
	Percent float64 `json:"percent"`
}

func LadderHeights(srcH int) []int {
	rungs := pickRungs(srcH)
	out := make([]int, len(rungs))
	for i, r := range rungs {
		out[i] = r.Height
	}
	return out
}

func (p JobProgress) Map() map[string]any {
	rungs := p.Rungs
	if rungs == nil {
		rungs = []int{}
	}
	return map[string]any{
		"rungs":   rungs,
		"index":   p.Index,
		"ratio":   p.Ratio,
		"percent": p.Percent,
	}
}

func writeProgress(dir string, rungs []int, index int, ratio float64) {
	p := makeProgress(rungs, index, ratio)
	b, err := json.Marshal(p)
	if err != nil {
		return
	}
	tmp := filepath.Join(dir, "progress.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, filepath.Join(dir, "progress.json"))
}

func makeProgress(rungs []int, index int, ratio float64) JobProgress {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	if index < 0 {
		index = 0
	}
	n := len(rungs)
	if n == 0 {
		return JobProgress{Rungs: []int{}, Ratio: ratio, Percent: ratio * 100}
	}
	if index >= n {
		index = n - 1
		ratio = 1
	}
	pct := (float64(index) + ratio) / float64(n) * 100
	if pct > 100 {
		pct = 100
	}
	return JobProgress{Rungs: rungs, Index: index, Ratio: ratio, Percent: pct}
}

func InspectProgress(fs *storage.Local, v store.Video) JobProgress {
	if v.Status == "ready" {
		p := JobProgress{Percent: 100, Ratio: 1}
		if dir, err := fs.Abs(filepath.ToSlash(filepath.Join("hls", v.ID))); err == nil {
			if cached := readProgressFile(dir); len(cached.Rungs) > 0 {
				p.Rungs = cached.Rungs
				p.Index = len(cached.Rungs) - 1
			} else {
				p.Rungs = listHeightDirs(dir)
				if n := len(p.Rungs); n > 0 {
					p.Index = n - 1
				}
			}
		}
		return p
	}
	if v.Status != "processing" {
		return JobProgress{Rungs: []int{}}
	}
	dir, err := fs.Abs(filepath.ToSlash(filepath.Join("hls", v.ID)))
	if err != nil {
		return JobProgress{Rungs: []int{}}
	}
	p := readProgressFile(dir)
	durMs := 0
	if v.DurationMs != nil {
		durMs = *v.DurationMs
	}
	srcH := 0
	if v.Height != nil {
		srcH = *v.Height
	}
	if (len(p.Rungs) == 0 || durMs == 0) && v.SourceObjectKey != nil {
		if src, err := fs.Abs(*v.SourceObjectKey); err == nil {
			if info, err := ffprobe(context.Background(), src); err == nil {
				if srcH == 0 {
					srcH = info.Height
				}
				if durMs == 0 {
					durMs = info.DurationMs
				}
			}
		}
	}
	if len(p.Rungs) == 0 {
		if srcH > 0 {
			p.Rungs = LadderHeights(srcH)
		} else {
			p.Rungs = listHeightDirs(dir)
		}
	}
	if len(p.Rungs) == 0 {
		return p
	}
	expected := durationSegs(durMs)
	if expected == 0 {
		for _, h := range p.Rungs {
			sub := filepath.Join(dir, strconv.Itoa(h))
			b, err := os.ReadFile(filepath.Join(sub, "index.m3u8"))
			if err == nil && strings.Contains(string(b), "EXT-X-ENDLIST") {
				if n := countSegs(sub); n > expected {
					expected = n
				}
			}
		}
	}
	idx, ratio := 0, 0.0
	for i, h := range p.Rungs {
		sub := filepath.Join(dir, strconv.Itoa(h))
		r := diskRungRatio(sub, expected)
		if r >= 0.999 {
			idx, ratio = i, 1
			continue
		}
		idx = i
		if r > 0 {
			ratio = r
		} else {
			ratio = 0
		}
		break
	}
	if disk := makeProgress(p.Rungs, idx, ratio); disk.Percent >= p.Percent {
		return disk
	}
	return makeProgress(p.Rungs, p.Index, p.Ratio)
}

func readProgressFile(dir string) JobProgress {
	b, err := os.ReadFile(filepath.Join(dir, "progress.json"))
	if err != nil {
		return JobProgress{}
	}
	var p JobProgress
	if json.Unmarshal(b, &p) != nil {
		return JobProgress{}
	}
	return p
}

func listHeightDirs(dir string) []int {
	var out []int
	for _, h := range []int{360, 720, 1080} {
		if dirExists(filepath.Join(dir, strconv.Itoa(h))) {
			out = append(out, h)
		}
	}
	return out
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func countSegs(dir string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, "seg_*.ts"))
	return len(matches)
}

func durationSegs(durationMs int) int {
	if durationMs <= 0 {
		return 0
	}
	n := durationMs / 4000
	if n < 1 {
		return 1
	}
	return n
}

func diskRungRatio(dir string, expected int) float64 {
	b, err := os.ReadFile(filepath.Join(dir, "index.m3u8"))
	if err == nil && strings.Contains(string(b), "EXT-X-ENDLIST") {
		return 1
	}
	n := countSegs(dir)
	if n == 0 || expected < 1 {
		return 0
	}
	r := float64(n) / float64(expected)
	if r > 1 {
		return 1
	}
	return r
}
