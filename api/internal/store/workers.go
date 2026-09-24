package store

import (
	"context"
)

func (p *Postgres) UpsertTranscodeWorker(ctx context.Context, id string, rank int, encoder string) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO transcode_workers (id, rank, encoder, busy, healthy, last_seen)
		VALUES ($1, $2, $3, false, true, now())
		ON CONFLICT (id) DO UPDATE SET
			rank = EXCLUDED.rank,
			encoder = EXCLUDED.encoder,
			busy = false,
			healthy = true,
			claimed_video_id = NULL,
			last_seen = now()`, id, rank, encoder)
	return err
}

func (p *Postgres) TouchTranscodeWorker(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `UPDATE transcode_workers SET last_seen = now() WHERE id = $1`, id)
	return err
}

func (p *Postgres) SetTranscodeWorkerIdle(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE transcode_workers
		SET busy = false, claimed_video_id = NULL, last_seen = now()
		WHERE id = $1`, id)
	return err
}

func (p *Postgres) MarkTranscodeWorkerUnhealthy(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE transcode_workers
		SET healthy = false, busy = false, claimed_video_id = NULL, last_seen = now()
		WHERE id = $1`, id)
	return err
}

func (p *Postgres) ReleaseTranscodeClaim(ctx context.Context, videoID string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE videos SET transcode_claimed_at = NULL, updated_at = now()
		WHERE id = $1 AND status = 'processing'`, videoID)
	return err
}
