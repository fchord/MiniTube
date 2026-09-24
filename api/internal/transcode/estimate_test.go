package transcode

import (
	"testing"
	"time"
)

func TestBudgetScalesWithDurationAndRungs(t *testing.T) {
	m := &SpeedModel{
		HeightSpeed: map[int]float64{360: 10, 720: 5, 1080: 1},
		AudioSpeed:  50,
	}
	src := time.Hour
	one := m.work(src, []int{360}, false)
	two := m.work(src, []int{360, 720}, false)
	if two <= one {
		t.Fatalf("two rungs should take longer: %s vs %s", two, one)
	}
	// 360 @ 10x => 6m; 720 @ 5x => 12m; overhead 45s
	want := 6*time.Minute + 12*time.Minute + fixedOverhead
	if two < want-time.Second || two > want+time.Second {
		t.Fatalf("work=%s want=%s", two, want)
	}
	b := m.Budget(src, []int{360, 720}, true)
	if b < minJobTimeout {
		t.Fatalf("budget %s below floor", b)
	}
	if b > maxJobTimeout {
		t.Fatalf("budget %s above cap", b)
	}
	short := m.Budget(2*time.Second, []int{360}, false)
	if short != minJobTimeout {
		t.Fatalf("short clip should use floor, got %s", short)
	}
}

func TestSpeedFallbackScalesByPixels(t *testing.T) {
	m := &SpeedModel{HeightSpeed: map[int]float64{720: 4}}
	s1080 := m.speed(1080)
	s720 := m.speed(720)
	if s1080 >= s720 {
		t.Fatalf("1080 should be slower than 720, got %v vs %v", s1080, s720)
	}
}
