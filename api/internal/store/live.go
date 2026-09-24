package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Live struct {
	ID            uuid.UUID
	ChannelID     uuid.UUID
	Channel       Channel
	Title         string
	Description   string
	Status        string
	Visibility    string
	IngestKey     string
	IngestKeyHash string
	PlaybackURL   *string
	ThumbnailURL  *string
	ScheduledAt   *time.Time
	StartedAt     *time.Time
	EndedAt       *time.Time
	VODVideoID    *string
	ArchiveBytes  *int64
	ViewerCount   int
	LikeCount     int64
	UnpublishedAt *time.Time
	LastHLSAt     *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const liveSelect = `
	SELECT l.id, l.channel_id, l.title, l.description, l.status, l.visibility, COALESCE(l.ingest_key, ''), l.ingest_key_hash,
	       l.playback_url, l.thumbnail_url, l.scheduled_at, l.started_at, l.ended_at, l.vod_video_id,
	       l.archive_bytes, l.viewer_count, l.like_count, l.unpublished_at, l.last_hls_at, l.created_at, l.updated_at
	FROM live_streams l`

const liveReturning = `id, channel_id, title, description, status, visibility, COALESCE(ingest_key, ''), ingest_key_hash,
		          playback_url, thumbnail_url, scheduled_at, started_at, ended_at, vod_video_id,
		          archive_bytes, viewer_count, like_count, unpublished_at, last_hls_at, created_at, updated_at`

func scanLive(row interface{ Scan(dest ...any) error }) (Live, error) {
	var l Live
	err := row.Scan(&l.ID, &l.ChannelID, &l.Title, &l.Description, &l.Status, &l.Visibility, &l.IngestKey, &l.IngestKeyHash,
		&l.PlaybackURL, &l.ThumbnailURL, &l.ScheduledAt, &l.StartedAt, &l.EndedAt, &l.VODVideoID,
		&l.ArchiveBytes, &l.ViewerCount, &l.LikeCount, &l.UnpublishedAt, &l.LastHLSAt, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Live{}, ErrNotFound
	}
	return l, err
}

func HashIngestKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (p *Postgres) CreateLive(ctx context.Context, channelID uuid.UUID, title, description string, scheduledAt *time.Time, ingestKey, playbackURL string) (Live, error) {
	l, err := scanLive(p.pool.QueryRow(ctx, `
		INSERT INTO live_streams (channel_id, title, description, status, ingest_key_hash, ingest_key, playback_url, scheduled_at)
		VALUES ($1, $2, $3, 'scheduled', $4, $5, $6, $7)
		RETURNING `+liveReturning,
		channelID, title, description, HashIngestKey(ingestKey), ingestKey, playbackURL, scheduledAt,
	))
	if err != nil {
		return Live{}, err
	}
	ch, err := p.GetChannelByID(ctx, channelID)
	if err != nil {
		return Live{}, err
	}
	l.Channel = ch
	l.IngestKey = ingestKey
	return l, nil
}

func (p *Postgres) GetLive(ctx context.Context, id uuid.UUID) (Live, error) {
	l, err := scanLive(p.pool.QueryRow(ctx, liveSelect+" WHERE l.id = $1", id))
	if err != nil {
		return Live{}, err
	}
	ch, err := p.GetChannelByID(ctx, l.ChannelID)
	if err != nil {
		return Live{}, err
	}
	l.Channel = ch
	return l, nil
}

func (p *Postgres) GetLiveByIngestKey(ctx context.Context, key string) (Live, error) {
	l, err := scanLive(p.pool.QueryRow(ctx, liveSelect+" WHERE l.ingest_key = $1", key))
	if err != nil {
		return Live{}, err
	}
	return p.GetLive(ctx, l.ID)
}

func visibleLiveSQL(ownerView bool) string {
	if ownerView {
		return ` AND l.visibility IN ('public', 'private')`
	}
	return ` AND l.visibility = 'public'`
}

func (p *Postgres) ListChannelLives(ctx context.Context, channelID uuid.UUID, ownerView bool, limit int) ([]Live, error) {
	return p.listLives(ctx, liveSelect+` WHERE l.channel_id = $1`+visibleLiveSQL(ownerView)+` ORDER BY l.created_at DESC LIMIT $2`, channelID, limit)
}

func (p *Postgres) ListDeletedLives(ctx context.Context, channelID uuid.UUID, limit int) ([]Live, error) {
	return p.listLives(ctx, liveSelect+` WHERE l.channel_id = $1 AND l.visibility = 'deleted' ORDER BY l.updated_at DESC LIMIT $2`, channelID, limit)
}

func (p *Postgres) ListPublicLive(ctx context.Context, limit int) ([]Live, error) {
	return p.listLives(ctx, liveSelect+`
		JOIN channels c ON c.id = l.channel_id
		WHERE l.status = 'live' AND l.visibility = 'public' AND c.visibility = 'public'
		ORDER BY l.started_at DESC NULLS LAST LIMIT $1`, limit)
}

func (p *Postgres) ListOpenLives(ctx context.Context) ([]Live, error) {
	rows, err := p.pool.Query(ctx, liveSelect+` WHERE l.status IN ('scheduled', 'live') AND l.visibility <> 'deleted' AND l.ingest_key IS NOT NULL AND l.ingest_key <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Live
	for rows.Next() {
		l, err := scanLive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (p *Postgres) listLives(ctx context.Context, q string, args ...any) ([]Live, error) {
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Live
	for rows.Next() {
		l, err := scanLive(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hydrated := make([]Live, 0, len(out))
	for _, l := range out {
		ch, err := p.GetChannelByID(ctx, l.ChannelID)
		if err != nil {
			continue
		}
		l.Channel = ch
		hydrated = append(hydrated, l)
	}
	return hydrated, nil
}

func (p *Postgres) MarkLiveStarted(ctx context.Context, id uuid.UUID, playbackURL string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE live_streams SET
			status = 'live',
			playback_url = $2,
			started_at = COALESCE(started_at, now()),
			unpublished_at = NULL,
			updated_at = now()
		WHERE id = $1 AND status IN ('scheduled', 'live')`, id, playbackURL)
	return err
}

func (p *Postgres) MarkLiveUnpublished(ctx context.Context, id uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE live_streams SET
			unpublished_at = COALESCE(unpublished_at, now()),
			updated_at = now()
		WHERE id = $1 AND status = 'live'`, id)
	return err
}

func (p *Postgres) TouchLiveHLS(ctx context.Context, id uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `UPDATE live_streams SET last_hls_at = now() WHERE id = $1`, id)
	return err
}

func (p *Postgres) MarkLiveEnded(ctx context.Context, id uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE live_streams SET
			status = 'ended',
			ended_at = COALESCE(ended_at, now()),
			updated_at = now()
		WHERE id = $1 AND status <> 'ended'`, id)
	return err
}

func (p *Postgres) SetLiveVOD(ctx context.Context, liveID uuid.UUID, videoID string) error {
	_, err := p.pool.Exec(ctx, `UPDATE live_streams SET vod_video_id = $2, updated_at = now() WHERE id = $1`, liveID, videoID)
	return err
}

func (p *Postgres) SetLiveArchiveBytes(ctx context.Context, id uuid.UUID, n int64) error {
	if n < 1 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `
		UPDATE live_streams SET archive_bytes = $2
		WHERE id = $1 AND (archive_bytes IS NULL OR archive_bytes < $2)`, id, n)
	return err
}

func (p *Postgres) SetLiveThumbnail(ctx context.Context, liveID uuid.UUID, url string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE live_streams SET thumbnail_url = $2, updated_at = now()
		WHERE id = $1 AND (thumbnail_url IS NULL OR thumbnail_url = '')`, liveID, url)
	return err
}

func (p *Postgres) ListLivesMissingThumbnail(ctx context.Context, limit int) ([]Live, error) {
	return p.listLives(ctx, liveSelect+`
		WHERE (l.thumbnail_url IS NULL OR l.thumbnail_url = '')
		  AND l.visibility <> 'deleted'
		  AND l.ingest_key IS NOT NULL AND l.ingest_key <> ''
		ORDER BY l.created_at DESC LIMIT $1`, limit)
}

func (p *Postgres) SetLiveVisibility(ctx context.Context, id uuid.UUID, visibility string) (Live, error) {
	if visibility != "public" && visibility != "private" && visibility != "deleted" {
		return Live{}, errors.New("invalid visibility")
	}
	tag, err := p.pool.Exec(ctx, `UPDATE live_streams SET visibility = $2, updated_at = now() WHERE id = $1`, id, visibility)
	if err != nil {
		return Live{}, err
	}
	if tag.RowsAffected() == 0 {
		return Live{}, ErrNotFound
	}
	return p.GetLive(ctx, id)
}

func (p *Postgres) DeleteLive(ctx context.Context, id uuid.UUID) error {
	if err := p.DeleteEngagement(ctx, "live", id.String()); err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `DELETE FROM live_streams WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) LiveLiked(ctx context.Context, userID, liveID uuid.UUID) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM likes WHERE user_id = $1 AND target_type = 'live' AND target_id = $2)`, userID, liveID).Scan(&ok)
	return ok, err
}
