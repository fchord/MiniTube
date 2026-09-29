package transcode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"minitube/api/internal/storage"
	"minitube/api/internal/store"
)

type JobProgress struct {
	Rungs   []string `json:"rungs"`
	Index   int      `json:"index"`
	Ratio   float64  `json:"ratio"`
	Percent float64  `json:"percent"`
	Codec   string   `json:"codec,omitempty"`
	Height  int      `json:"height,omitempty"`
}

func LadderHeights(srcW, srcH int) []int {
	rungs := pickRungs(srcW, srcH)
	out := make([]int, 0, len(rungs))
	for _, r := range rungs {
		out = append(out, r.Height)
	}
	return out
}

func LadderLabels(srcW, srcH int) []string {
	rungs := pickRungs(srcW, srcH)
	out := make([]string, len(rungs))
	for i, r := range rungs {
		out[i] = r.Label()
	}
	return out
}

func (p JobProgress) Map() map[string]any {
	rungs := p.Rungs
	if rungs == nil {
		rungs = []string{}
	}
	m := map[string]any{
		"rungs":   rungs,
		"index":   p.Index,
		"ratio":   p.Ratio,
		"percent": p.Percent,
	}
	if p.Codec != "" {
		m["codec"] = p.Codec
	}
	if p.Height > 0 {
		m["height"] = p.Height
	}
	return m
}

func writeProgress(dir string, rungs []string, index int, ratio float64, cur ladderRung) {
	p := makeProgress(rungs, index, ratio)
	p.Codec = cur.Label()
	p.Height = cur.Height
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

func makeProgress(rungs []string, index int, ratio float64) JobProgress {
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
		return JobProgress{Rungs: []string{}, Ratio: ratio, Percent: ratio * 100}
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
				if cached.Codec != "" {
					p.Codec = cached.Codec
				} else if n := len(p.Rungs); n > 0 {
					p.Codec = p.Rungs[n-1]
				}
				p.Height = cached.Height
			} else {
				p.Rungs = listRungLabels(dir)
				if n := len(p.Rungs); n > 0 {
					p.Index = n - 1
					p.Codec = p.Rungs[n-1]
				}
			}
		}
		return p
	}
	if v.Status != "processing" {
		return JobProgress{Rungs: []string{}}
	}
	dir, err := fs.Abs(filepath.ToSlash(filepath.Join("hls", v.ID)))
	if err != nil {
		return JobProgress{Rungs: []string{}}
	}
	p := readProgressFile(dir)
	if len(p.Rungs) > 0 {
		out := makeProgress(p.Rungs, p.Index, p.Ratio)
		out.Codec = p.Codec
		out.Height = p.Height
		if out.Codec == "" && out.Index >= 0 && out.Index < len(p.Rungs) {
			out.Codec = p.Rungs[out.Index]
		}
		return out
	}
	srcW, srcH := 0, 0
	if v.Width != nil {
		srcW = *v.Width
	}
	if v.Height != nil {
		srcH = *v.Height
	}
	if srcW == 0 || srcH == 0 {
		p.Rungs = listRungLabels(dir)
	} else {
		p.Rungs = LadderLabels(srcW, srcH)
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

func listRungLabels(dir string) []string {
	var out []string
	type fam struct{ dir, lab string }
	for _, f := range []fam{{"avc", "H.264"}, {"hevc", "H.265"}} {
		for _, h := range []int{2160, 1440, 1080, 720, 480, 360, 240, 144} {
			if dirExists(filepath.Join(dir, f.dir, strconv.Itoa(h))) {
				out = append(out, fmt.Sprintf("%s %dp", f.lab, h))
			}
		}
	}
	return out
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
