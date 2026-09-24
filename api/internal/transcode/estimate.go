package transcode

import (
	"encoding/json"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	safetyFactor     = 2.5
	minJobTimeout    = 10 * time.Minute
	maxJobTimeout    = 12 * time.Hour
	fixedOverhead    = 45 * time.Second
	speedEMA         = 0.35
	maxRealtimeSpeed = 30.0
	minRealtimeSpeed = 0.15
)

// SpeedModel is this machine's measured encode rate, in multiples of realtime.
// speed=4 means a 1 hour source takes ~15 minutes for that pass.
type SpeedModel struct {
	Encoder     string          `json:"encoder"`
	Preset      string          `json:"preset"`
	CPU         string          `json:"cpu"`
	Nproc       int             `json:"nproc"`
	GPU         string          `json:"gpu"`
	HeightSpeed map[int]float64 `json:"heightSpeed"`
	AudioSpeed  float64         `json:"audioSpeed"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

var (
	modelMu   sync.Mutex
	model     = defaultSpeedModel()
	modelPath string
)

func defaultSpeedModel() *SpeedModel {
	return &SpeedModel{
		Encoder: activeEncoder(),
		Preset:  getenv("TRANSCODE_PRESET", "veryfast"),
		CPU:     cpuModel(),
		Nproc:   runtime.NumCPU(),
		GPU:     "see TRANSCODE_ENCODER",
		// Conservative software-encode defaults (circa 4c/8t). Calibrate overwrites.
		HeightSpeed: map[int]float64{360: 4, 720: 1.5, 1080: 0.7},
		AudioSpeed:  25,
	}
}

func currentModel() *SpeedModel {
	modelMu.Lock()
	defer modelMu.Unlock()
	return cloneModel(model)
}

func setModel(m *SpeedModel, path string) {
	modelMu.Lock()
	defer modelMu.Unlock()
	model = cloneModel(m)
	modelPath = path
}

func cloneModel(m *SpeedModel) *SpeedModel {
	if m == nil {
		return defaultSpeedModel()
	}
	cp := *m
	cp.HeightSpeed = map[int]float64{}
	for k, v := range m.HeightSpeed {
		cp.HeightSpeed[k] = v
	}
	return &cp
}

func (m *SpeedModel) speed(height int) float64 {
	if m == nil {
		return 1
	}
	if v := m.HeightSpeed[height]; v > 0 {
		return clampSpeed(v)
	}
	if ref := m.HeightSpeed[720]; ref > 0 && height > 0 {
		// Encode cost roughly tracks pixel count.
		return clampSpeed(ref * (720.0 * 720.0) / float64(height*height))
	}
	return 1
}

func (m *SpeedModel) audioSpeed() float64 {
	if m == nil || m.AudioSpeed <= 0 {
		return 25
	}
	return clampSpeed(m.AudioSpeed)
}

func (m *SpeedModel) work(src time.Duration, heights []int, hasAudio bool) time.Duration {
	if src <= 0 {
		return time.Minute
	}
	var w float64
	for _, h := range heights {
		w += float64(src) / m.speed(h)
	}
	if hasAudio {
		w += float64(src) / m.audioSpeed()
	}
	w += float64(fixedOverhead)
	return time.Duration(w)
}

func (m *SpeedModel) Budget(src time.Duration, heights []int, hasAudio bool) time.Duration {
	b := time.Duration(float64(m.work(src, heights, hasAudio)) * safetyFactor)
	if b < minJobTimeout {
		b = minJobTimeout
	}
	if b > maxJobTimeout {
		b = maxJobTimeout
	}
	if s := os.Getenv("TRANSCODE_JOB_TIMEOUT"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 && d < b {
			b = d
		}
	}
	return b
}

func recordRung(height int, src, wall time.Duration) {
	if src < 2*time.Second || wall < 200*time.Millisecond {
		return
	}
	measured := float64(src) / float64(wall)
	modelMu.Lock()
	if model.HeightSpeed == nil {
		model.HeightSpeed = map[int]float64{}
	}
	old := model.HeightSpeed[height]
	if old <= 0 {
		model.HeightSpeed[height] = clampSpeed(measured)
	} else {
		model.HeightSpeed[height] = clampSpeed(speedEMA*measured + (1-speedEMA)*old)
	}
	model.UpdatedAt = time.Now()
	path := modelPath
	snap := cloneModel(model)
	modelMu.Unlock()
	if path != "" {
		_ = saveSpeedModel(path, snap)
	}
}

func recordAudio(src, wall time.Duration) {
	if src < 2*time.Second || wall < 200*time.Millisecond {
		return
	}
	measured := float64(src) / float64(wall)
	modelMu.Lock()
	old := model.AudioSpeed
	if old <= 0 {
		model.AudioSpeed = clampSpeed(measured)
	} else {
		model.AudioSpeed = clampSpeed(speedEMA*measured + (1-speedEMA)*old)
	}
	model.UpdatedAt = time.Now()
	path := modelPath
	snap := cloneModel(model)
	modelMu.Unlock()
	if path != "" {
		_ = saveSpeedModel(path, snap)
	}
}

func clampSpeed(v float64) float64 {
	if v > maxRealtimeSpeed {
		return maxRealtimeSpeed
	}
	if v < minRealtimeSpeed {
		return minRealtimeSpeed
	}
	return v
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return runtime.GOARCH
}

func saveSpeedModel(path string, m *SpeedModel) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func loadSpeedModel(path string) (*SpeedModel, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m SpeedModel
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.HeightSpeed == nil {
		m.HeightSpeed = map[int]float64{}
	}
	return &m, nil
}

func (m *SpeedModel) fingerprint(preset string) string {
	return strings.Join([]string{m.Encoder, preset, m.CPU, strconv.Itoa(m.Nproc)}, "|")
}
