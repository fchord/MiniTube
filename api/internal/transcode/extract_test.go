package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestExtractJPEG(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	dst := filepath.Join(dir, "thumb.jpg")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=red:s=320x180:d=1", "-frames:v", "8", src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make src: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ExtractJPEG(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() < 100 {
		t.Fatalf("thumb missing or tiny: %v %#v", err, st)
	}
}
