package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"minitube/api/internal/ident"
)

type Video struct {
	ID               string
	ChannelID        uuid.UUID
	Kind             string
	Title            string
	Description      string
	Status           string
	Visibility       string
	DurationMs       *int
	Width            *int
	Height           *int
	AspectRatio      *float64
	ThumbnailURL     *string
	PreviewSpriteURL *string
	SourceObjectKey  *string
	SourceBytes      *int64
	ErrorMessage     *string
	ViewCount        int64
	LikeCount        int64
	CommentCount     int64
	PublishedAt      *time.Time
	ScheduledAt      *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
	Channel          Channel
}

type Rendition struct {
	Height       int
	BandwidthBps int
	PlaylistKey  string
	Codec        string
}

type AudioTrack struct {
	Language    string
	Label       string
	IsDefault   bool
	PlaylistKey string
}

type SubtitleTrack struct {
	Language  string
	Label     string
	IsDefault bool
	VTTKey    string
}

const videoSelect = `
	SELECT v.id, v.channel_id, v.kind, v.title, v.description, v.status, v.visibility, v.duration_ms, v.width, v.height,
	       v.aspect_ratio, v.thumbnail_url, v.preview_sprite_url, v.source_object_key, v.source_bytes, v.error_message,
	       v.view_count, v.like_count, v.comment_count, v.published_at, v.scheduled_at, v.created_at, v.updated_at
	FROM videos v`

const videoReturning = `id, channel_id, kind, title, description, status, visibility, duration_ms, width, height,
		          aspect_ratio, thumbnail_url, preview_sprite_url, source_object_key, source_bytes, error_message,
		          view_count, like_count, comment_count, published_at, scheduled_at, created_at, updated_at`

func scanVideo(row interface{ Scan(dest ...any) error }) (Video, error) {
	var v Video
	err := row.Scan(&v.ID, &v.ChannelID, &v.Kind, &v.Title, &v.Description, &v.Status, &v.Visibility, &v.DurationMs, &v.Width, &v.Height,
		&v.AspectRatio, &v.ThumbnailURL, &v.PreviewSpriteURL, &v.SourceObjectKey, &v.SourceBytes, &v.ErrorMessage,
		&v.ViewCount, &v.LikeCount, &v.CommentCount, &v.PublishedAt, &v.ScheduledAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Video{}, ErrNotFound
	}
	return v, err
}

func (p *Postgres) CreateVideo(ctx context.Context, channelID uuid.UUID, kind, title, description, visibility, sourceKey string, sourceBytes int64) (Video, error) {
	if visibility != "private" {
		visibility = "public"
	}
	var bytes any
	if sourceBytes > 0 {
		bytes = sourceBytes
	}
	var v Video
	var err error
	for n := 0; n < 8; n++ {
		id := ident.NewVideoID()
		v, err = scanVideo(p.pool.QueryRow(ctx, `
			INSERT INTO videos (id, channel_id, kind, title, description, status, visibility, source_object_key, source_bytes)
			VALUES ($1, $2, $3, $4, $5, 'uploading', $6, $7, $8)
			RETURNING `+videoReturning,
			id, channelID, kind, title, description, visibility, sourceKey, bytes,
		))
		if err == nil {
			break
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			continue
		}
		return Video{}, err
	}
	if err != nil {
		return Video{}, err
	}
	ch, err := p.GetChannelByID(ctx, channelID)
	if err != nil {
		return Video{}, err
	}
	v.Channel = ch
	return v, nil
}

func (p *Postgres) ResolveVideoID(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if ident.ValidVideoID(raw) {
		var id string
		err := p.pool.QueryRow(ctx, `SELECT id FROM videos WHERE id = $1`, raw).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return id, err
	}
	old, err := uuid.Parse(raw)
	if err != nil {
		return "", ErrNotFound
	}
	var id string
	err = p.pool.QueryRow(ctx, `SELECT video_id FROM video_id_legacy WHERE old_id = $1`, old).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

func (p *Postgres) GetVideo(ctx context.Context, id string) (Video, error) {
	v, err := scanVideo(p.pool.QueryRow(ctx, videoSelect+" WHERE v.id = $1", id))
	if err != nil {
		return Video{}, err
	}
	ch, err := p.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		return Video{}, err
	}
	v.Channel = ch
	return v, nil
}

func (p *Postgres) ListChannelVideos(ctx context.Context, channelID uuid.UUID, kind string, ownerView bool, limit int) ([]Video, error) {
	q := videoSelect + ` WHERE v.channel_id = $1 AND v.kind = $2` + visibleVideoSQL(ownerView)
	q += ` ORDER BY COALESCE(v.published_at, v.created_at) DESC LIMIT $3`
	rows, err := p.pool.Query(ctx, q, channelID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if out == nil {
		out = []Video{}
	}
	return out, rows.Err()
}

func (p *Postgres) ListRandomFeedShorts(ctx context.Context, exclude []string, limit int) ([]Video, error) {
	q := videoSelect + `
		JOIN channels c ON c.id = v.channel_id
		WHERE v.kind = 'short' AND v.status = 'ready'
		  AND v.visibility = 'public'
		  AND c.visibility = 'public'`
	args := []any{}
	if len(exclude) > 0 {
		q += ` AND v.id <> ALL($1) ORDER BY random() LIMIT $2`
		args = append(args, exclude, limit)
	} else {
		q += ` ORDER BY random() LIMIT $1`
		args = append(args, limit)
	}
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hydrateVideoChannels(ctx, p, out)
}

func (p *Postgres) ListFeedShorts(ctx context.Context, cursorT time.Time, cursorID string, limit int) ([]Video, error) {
	q := videoSelect + `
		JOIN channels c ON c.id = v.channel_id
		WHERE v.kind = 'short' AND v.status = 'ready'
		  AND v.visibility = 'public'
		  AND c.visibility = 'public'`
	args := []any{}
	if !cursorT.IsZero() && cursorID != "" {
		q += ` AND (COALESCE(v.published_at, v.created_at), v.id) < ($1, $2)
			ORDER BY COALESCE(v.published_at, v.created_at) DESC, v.id DESC LIMIT $3`
		args = append(args, cursorT, cursorID, limit)
	} else {
		q += ` ORDER BY COALESCE(v.published_at, v.created_at) DESC, v.id DESC LIMIT $1`
		args = append(args, limit)
	}
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hydrateVideoChannels(ctx, p, out)
}

func hydrateVideoChannels(ctx context.Context, p *Postgres, items []Video) ([]Video, error) {
	if items == nil {
		return []Video{}, nil
	}
	out := make([]Video, 0, len(items))
	for _, v := range items {
		ch, err := p.GetChannelByID(ctx, v.ChannelID)
		if err != nil {
			continue
		}
		v.Channel = ch
		out = append(out, v)
	}
	return out, nil
}

func (p *Postgres) UpdateVideoMeta(ctx context.Context, id string, title, description, status *string, scheduledAt *time.Time) (Video, error) {
	_, err := p.pool.Exec(ctx, `
		UPDATE videos SET
			title = COALESCE($2, title),
			description = COALESCE($3, description),
			status = COALESCE($4, status),
			scheduled_at = COALESCE($5, scheduled_at),
			updated_at = now()
		WHERE id = $1`, id, title, description, status, scheduledAt)
	if err != nil {
		return Video{}, err
	}
	return p.GetVideo(ctx, id)
}

func (p *Postgres) MarkProcessing(ctx context.Context, id string) (Video, error) {
	_, err := p.pool.Exec(ctx, `
		UPDATE videos SET status = 'processing', transcode_claimed_at = NULL, error_message = NULL, updated_at = now()
		WHERE id = $1 AND status IN ('uploading', 'draft', 'failed')`, id)
	if err != nil {
		return Video{}, err
	}
	return p.GetVideo(ctx, id)
}

func (p *Postgres) SetSourceKey(ctx context.Context, id string, key string) error {
	_, err := p.pool.Exec(ctx, `UPDATE videos SET source_object_key = $2, updated_at = now() WHERE id = $1`, id, key)
	return err
}

func (p *Postgres) SetSourceBytes(ctx context.Context, id string, n int64) error {
	if n < 1 {
		return nil
	}
	_, err := p.pool.Exec(ctx, `UPDATE videos SET source_bytes = $2 WHERE id = $1 AND source_bytes IS NULL`, id, n)
	return err
}

func (p *Postgres) SetVideoVisibility(ctx context.Context, id string, visibility string) (Video, error) {
	if visibility != "public" && visibility != "private" && visibility != "deleted" {
		return Video{}, errors.New("invalid visibility")
	}
	tag, err := p.pool.Exec(ctx, `UPDATE videos SET visibility = $2, updated_at = now() WHERE id = $1`, id, visibility)
	if err != nil {
		return Video{}, err
	}
	if tag.RowsAffected() == 0 {
		return Video{}, ErrNotFound
	}
	return p.GetVideo(ctx, id)
}

func (p *Postgres) ListDeletedVideos(ctx context.Context, channelID uuid.UUID, kind string, limit int) ([]Video, error) {
	q := videoSelect + ` WHERE v.channel_id = $1 AND v.kind = $2 AND v.visibility = 'deleted' ORDER BY v.updated_at DESC LIMIT $3`
	rows, err := p.pool.Query(ctx, q, channelID, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Video
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if out == nil {
		out = []Video{}
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteVideo(ctx context.Context, id string) error {
	if err := p.DeleteEngagement(ctx, "video", id); err != nil {
		return err
	}
	tag, err := p.pool.Exec(ctx, `DELETE FROM videos WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) rankedWorkerAllowed(ctx context.Context, workerID string) (bool, error) {
	var allowed bool
	err := p.pool.QueryRow(ctx, `
		SELECT
		  EXISTS (SELECT 1 FROM transcode_workers WHERE id = $1 AND healthy)
		  AND NOT EXISTS (
		    SELECT 1
		    FROM transcode_workers me
		    JOIN transcode_workers w ON w.rank < me.rank
		    WHERE me.id = $1
		      AND w.healthy
		      AND NOT w.busy
		      AND w.last_seen > now() - interval '15 seconds'
		  )
	`, workerID).Scan(&allowed)
	return allowed, err
}

func (p *Postgres) ClaimTranscodeJob(ctx context.Context, workerID string) (Video, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Video{}, err
	}
	defer tx.Rollback(ctx)

	if workerID != "" {
		var allowed bool
		if err := tx.QueryRow(ctx, `
			SELECT
			  EXISTS (SELECT 1 FROM transcode_workers WHERE id = $1 AND healthy)
			  AND NOT EXISTS (
			    SELECT 1
			    FROM transcode_workers me
			    JOIN transcode_workers w ON w.rank < me.rank
			    WHERE me.id = $1
			      AND w.healthy
			      AND NOT w.busy
			      AND w.last_seen > now() - interval '15 seconds'
			  )
		`, workerID).Scan(&allowed); err != nil {
			return Video{}, err
		}
		if !allowed {
			return Video{}, ErrNotFound
		}
	}

	v, err := scanVideo(tx.QueryRow(ctx, `
		UPDATE videos SET transcode_claimed_at = now(), updated_at = now()
		WHERE id = (
			SELECT id FROM videos
			WHERE status = 'processing'
			  AND (transcode_claimed_at IS NULL OR transcode_claimed_at < now() - interval '20 minutes')
			ORDER BY created_at
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING `+videoReturning))
	if err != nil {
		return Video{}, err
	}
	if workerID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE transcode_workers
			SET busy = true, claimed_video_id = $2, last_seen = now()
			WHERE id = $1`, workerID, v.ID); err != nil {
			return Video{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Video{}, err
	}
	return v, nil
}

func (p *Postgres) TouchTranscodeClaim(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `
		UPDATE videos SET transcode_claimed_at = now()
		WHERE id = $1 AND status = 'processing'`, id)
	return err
}

type TranscodeResult struct {
	DurationMs   int
	Width        int
	Height       int
	AspectRatio  float64
	ThumbnailKey string
	Renditions   []Rendition
	Audio        []AudioTrack
	Subtitles    []SubtitleTrack
}

func (p *Postgres) SaveTranscodeSuccess(ctx context.Context, videoID string, res TranscodeResult) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	thumb := any(nil)
	if res.ThumbnailKey != "" {
		thumb = res.ThumbnailKey
	}
	_, err = tx.Exec(ctx, `
		UPDATE videos SET
			status = 'ready', duration_ms = $2, width = $3, height = $4, aspect_ratio = $5,
			thumbnail_url = $6, published_at = COALESCE(published_at, now()),
			error_message = NULL, transcode_claimed_at = NULL, updated_at = now()
		WHERE id = $1`, videoID, res.DurationMs, res.Width, res.Height, res.AspectRatio, thumb)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM video_renditions WHERE video_id = $1`, videoID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM audio_tracks WHERE video_id = $1`, videoID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM subtitle_tracks WHERE video_id = $1`, videoID); err != nil {
		return err
	}
	for _, r := range res.Renditions {
		if _, err := tx.Exec(ctx, `
			INSERT INTO video_renditions (video_id, height, bandwidth_bps, playlist_key, codec)
			VALUES ($1, $2, $3, $4, $5)`, videoID, r.Height, r.BandwidthBps, r.PlaylistKey, r.Codec); err != nil {
			return err
		}
	}
	for _, a := range res.Audio {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audio_tracks (video_id, language, label, is_default, playlist_key)
			VALUES ($1, $2, $3, $4, $5)`, videoID, a.Language, a.Label, a.IsDefault, a.PlaylistKey); err != nil {
			return err
		}
	}
	for _, s := range res.Subtitles {
		if _, err := tx.Exec(ctx, `
			INSERT INTO subtitle_tracks (video_id, language, label, is_default, vtt_key)
			VALUES ($1, $2, $3, $4, $5)`, videoID, s.Language, s.Label, s.IsDefault, s.VTTKey); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) SaveTranscodeFailure(ctx context.Context, videoID string, msg string) error {
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	_, err := p.pool.Exec(ctx, `
		UPDATE videos SET status = 'failed', error_message = $2, transcode_claimed_at = NULL, updated_at = now()
		WHERE id = $1`, videoID, msg)
	return err
}

func (p *Postgres) ListRenditions(ctx context.Context, videoID string) ([]Rendition, error) {
	rows, err := p.pool.Query(ctx, `SELECT height, bandwidth_bps, playlist_key, codec FROM video_renditions WHERE video_id = $1 ORDER BY height`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rendition
	for rows.Next() {
		var r Rendition
		if err := rows.Scan(&r.Height, &r.BandwidthBps, &r.PlaylistKey, &r.Codec); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Rendition{}
	}
	return out, rows.Err()
}

func (p *Postgres) ListAudioTracks(ctx context.Context, videoID string) ([]AudioTrack, error) {
	rows, err := p.pool.Query(ctx, `SELECT language, label, is_default, playlist_key FROM audio_tracks WHERE video_id = $1 ORDER BY is_default DESC`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AudioTrack
	for rows.Next() {
		var a AudioTrack
		if err := rows.Scan(&a.Language, &a.Label, &a.IsDefault, &a.PlaylistKey); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if out == nil {
		out = []AudioTrack{}
	}
	return out, rows.Err()
}

func (p *Postgres) ListSubtitleTracks(ctx context.Context, videoID string) ([]SubtitleTrack, error) {
	rows, err := p.pool.Query(ctx, `SELECT language, label, is_default, vtt_key FROM subtitle_tracks WHERE video_id = $1 ORDER BY is_default DESC`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubtitleTrack
	for rows.Next() {
		var s SubtitleTrack
		if err := rows.Scan(&s.Language, &s.Label, &s.IsDefault, &s.VTTKey); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if out == nil {
		out = []SubtitleTrack{}
	}
	return out, rows.Err()
}

func (p *Postgres) IncrementViews(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `UPDATE videos SET view_count = view_count + 1 WHERE id = $1`, id)
	return err
}

func (p *Postgres) OwnerHasProcessing(ctx context.Context, ownerID uuid.UUID) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM videos v
			JOIN channels c ON c.id = v.channel_id
			WHERE c.owner_user_id = $1 AND v.status = 'processing' AND v.visibility <> 'deleted'
		)`, ownerID).Scan(&ok)
	return ok, err
}

func (p *Postgres) DeleteOwnerUploading(ctx context.Context, ownerID uuid.UUID) ([]string, error) {
	rows, err := p.pool.Query(ctx, `
		DELETE FROM videos v
		USING channels c
		WHERE v.channel_id = c.id AND c.owner_user_id = $1 AND v.status = 'uploading'
		RETURNING v.id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (p *Postgres) Liked(ctx context.Context, userID uuid.UUID, videoID string) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM likes WHERE user_id = $1 AND target_type = 'video' AND target_id = $2)`, userID, videoID).Scan(&ok)
	return ok, err
}
