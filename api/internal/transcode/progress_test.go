package transcode

import "testing"

func TestMakeProgressEvenRungs(t *testing.T) {
	p := makeProgress([]int{360, 720}, 0, 0.5)
	if p.Percent < 24 || p.Percent > 26 {
		t.Fatalf("first half of first rung: %v", p.Percent)
	}
	p = makeProgress([]int{360, 720}, 1, 0.5)
	if p.Percent < 74 || p.Percent > 76 {
		t.Fatalf("half of second rung: %v", p.Percent)
	}
	p = makeProgress([]int{360, 720, 1080}, 2, 1)
	if p.Percent != 100 {
		t.Fatalf("all done: %v", p.Percent)
	}
}
