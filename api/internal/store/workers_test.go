package store

import (
	"context"
	"os"
	"testing"
	"time"

	"minitube/api/internal/db"
)

func TestRankedClaimGate(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://minitube:minitube@127.0.0.1:5433/minitube?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, url)
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := New(pool)
	suffix := time.Now().Format("150405.000")
	p1 := "test-p1-" + suffix
	p2 := "test-p2-" + suffix
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM transcode_workers WHERE id IN ($1, $2)`, p1, p2)
	})
	if err := st.UpsertTranscodeWorker(ctx, p1, 91, "h264_nvenc"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertTranscodeWorker(ctx, p2, 92, "h264_nvenc"); err != nil {
		t.Fatal(err)
	}

	ok, err := st.rankedWorkerAllowed(ctx, p2)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("P2 must wait while P1 is idle")
	}
	ok, err = st.rankedWorkerAllowed(ctx, p1)
	if err != nil || !ok {
		t.Fatalf("P1 should be allowed: %v %v", ok, err)
	}

	if _, err := pool.Exec(ctx, `UPDATE transcode_workers SET busy = true WHERE id = $1`, p1); err != nil {
		t.Fatal(err)
	}
	ok, err = st.rankedWorkerAllowed(ctx, p2)
	if err != nil || !ok {
		t.Fatalf("P2 should claim when P1 busy: %v %v", ok, err)
	}

	if err := st.MarkTranscodeWorkerUnhealthy(ctx, p1); err != nil {
		t.Fatal(err)
	}
	ok, err = st.rankedWorkerAllowed(ctx, p2)
	if err != nil || !ok {
		t.Fatalf("P2 should claim when P1 unhealthy: %v %v", ok, err)
	}
}
