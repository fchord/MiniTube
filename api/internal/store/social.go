package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Comment struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	User       User
	TargetType string
	TargetID   string
	ParentID   *uuid.UUID
	Body       string
	LikeCount  int64
	CreatedAt  time.Time
}

type WatchItem struct {
	Video         Video
	PositionMs    int
	DurationMs    *int
	Completed     bool
	LastWatchedAt time.Time
}

type FavoriteItem struct {
	TargetType string
	TargetID   string
	Video      *Video
	CreatedAt  time.Time
}

func (p *Postgres) Like(ctx context.Context, userID uuid.UUID, targetType string, targetID string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		INSERT INTO likes (user_id, target_type, target_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, userID, targetType, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err := bumpLike(ctx, tx, targetType, targetID, 1); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Unlike(ctx context.Context, userID uuid.UUID, targetType string, targetID string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM likes WHERE user_id = $1 AND target_type = $2 AND target_id = $3`, userID, targetType, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if err := bumpLike(ctx, tx, targetType, targetID, -1); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func bumpLike(ctx context.Context, tx pgx.Tx, targetType string, targetID string, delta int) error {
	q := ""
	switch targetType {
	case "video":
		q = `UPDATE videos SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1`
	case "post":
		q = `UPDATE posts SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1`
	case "live":
		q = `UPDATE live_streams SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1`
	case "comment":
		q = `UPDATE comments SET like_count = GREATEST(like_count + $2, 0) WHERE id = $1`
	default:
		return nil
	}
	_, err := tx.Exec(ctx, q, targetID, delta)
	return err
}

func (p *Postgres) UpsertProgress(ctx context.Context, userID uuid.UUID, videoID string, positionMs int, durationMs *int, completed bool) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO watch_history (user_id, video_id, position_ms, duration_ms, completed, last_watched_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id, video_id) DO UPDATE SET
			position_ms = GREATEST(watch_history.position_ms, EXCLUDED.position_ms),
			duration_ms = COALESCE(EXCLUDED.duration_ms, watch_history.duration_ms),
			completed = watch_history.completed OR EXCLUDED.completed,
			last_watched_at = now()`,
		userID, videoID, positionMs, durationMs, completed)
	return err
}

func (p *Postgres) ListWatchHistory(ctx context.Context, userID uuid.UUID, limit int) ([]WatchItem, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT w.position_ms, w.duration_ms, w.completed, w.last_watched_at, w.video_id
		FROM watch_history w
		WHERE w.user_id = $1
		ORDER BY w.last_watched_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WatchItem
	for rows.Next() {
		var it WatchItem
		var vid string
		if err := rows.Scan(&it.PositionMs, &it.DurationMs, &it.Completed, &it.LastWatchedAt, &vid); err != nil {
			return nil, err
		}
		v, err := p.GetVideo(ctx, vid)
		if err != nil {
			continue
		}
		it.Video = v
		out = append(out, it)
	}
	if out == nil {
		out = []WatchItem{}
	}
	return out, rows.Err()
}

func (p *Postgres) CreateComment(ctx context.Context, userID uuid.UUID, targetType string, targetID string, parent *uuid.UUID, body string) (Comment, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Comment{}, err
	}
	defer tx.Rollback(ctx)
	var c Comment
	err = tx.QueryRow(ctx, `
		INSERT INTO comments (user_id, target_type, target_id, parent_id, body)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, user_id, target_type, target_id, parent_id, body, like_count, created_at`,
		userID, targetType, targetID, parent, body,
	).Scan(&c.ID, &c.UserID, &c.TargetType, &c.TargetID, &c.ParentID, &c.Body, &c.LikeCount, &c.CreatedAt)
	if err != nil {
		return Comment{}, err
	}
	if targetType == "video" {
		if _, err := tx.Exec(ctx, `UPDATE videos SET comment_count = comment_count + 1 WHERE id = $1`, targetID); err != nil {
			return Comment{}, err
		}
	}
	if targetType == "post" {
		if _, err := tx.Exec(ctx, `UPDATE posts SET comment_count = comment_count + 1 WHERE id = $1`, targetID); err != nil {
			return Comment{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Comment{}, err
	}
	u, err := p.GetUserByID(ctx, userID)
	if err != nil {
		return Comment{}, err
	}
	c.User = u
	return c, nil
}

func (p *Postgres) ListComments(ctx context.Context, targetType string, targetID string, limit int) ([]Comment, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT c.id, c.user_id, c.target_type, c.target_id, c.parent_id, c.body, c.like_count, c.created_at
		FROM comments c
		WHERE c.target_type = $1 AND c.target_id = $2 AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC
		LIMIT $3`, targetType, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.UserID, &c.TargetType, &c.TargetID, &c.ParentID, &c.Body, &c.LikeCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		u, err := p.GetUserByID(ctx, c.UserID)
		if err != nil {
			return nil, err
		}
		c.User = u
		out = append(out, c)
	}
	if out == nil {
		out = []Comment{}
	}
	return out, rows.Err()
}

func (p *Postgres) ListMyComments(ctx context.Context, userID uuid.UUID, limit int) ([]Comment, error) {
	return p.listCommentsWhere(ctx, `user_id = $1 AND deleted_at IS NULL`, []any{userID}, limit)
}

func (p *Postgres) listCommentsWhere(ctx context.Context, where string, args []any, limit int) ([]Comment, error) {
	q := `SELECT id, user_id, target_type, target_id, parent_id, body, like_count, created_at FROM comments WHERE ` + where + ` ORDER BY created_at DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.UserID, &c.TargetType, &c.TargetID, &c.ParentID, &c.Body, &c.LikeCount, &c.CreatedAt); err != nil {
			return nil, err
		}
		u, err := p.GetUserByID(ctx, c.UserID)
		if err != nil {
			return nil, err
		}
		c.User = u
		out = append(out, c)
	}
	if out == nil {
		out = []Comment{}
	}
	return out, rows.Err()
}

func (p *Postgres) FavoriteListOwned(ctx context.Context, listID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM favorite_lists WHERE id = $1 AND user_id = $2)`, listID, userID).Scan(&ok)
	return ok, err
}

func (p *Postgres) AddFavorite(ctx context.Context, listID uuid.UUID, targetType string, targetID string) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO favorite_items (list_id, target_type, target_id) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, listID, targetType, targetID)
	return err
}

func (p *Postgres) RemoveFavorite(ctx context.Context, listID uuid.UUID, targetType string, targetID string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM favorite_items WHERE list_id = $1 AND target_type = $2 AND target_id = $3`, listID, targetType, targetID)
	return err
}

func (p *Postgres) ListFavoriteItems(ctx context.Context, listID uuid.UUID, limit int) ([]FavoriteItem, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT target_type, target_id, created_at FROM favorite_items
		WHERE list_id = $1 ORDER BY created_at DESC LIMIT $2`, listID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FavoriteItem
	for rows.Next() {
		var it FavoriteItem
		if err := rows.Scan(&it.TargetType, &it.TargetID, &it.CreatedAt); err != nil {
			return nil, err
		}
		if it.TargetType == "video" {
			v, err := p.GetVideo(ctx, it.TargetID)
			if err == nil {
				it.Video = &v
			}
		}
		out = append(out, it)
	}
	if out == nil {
		out = []FavoriteItem{}
	}
	return out, rows.Err()
}

func (p *Postgres) WatchLaterID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := p.pool.QueryRow(ctx, `SELECT id FROM favorite_lists WHERE user_id = $1 AND kind = 'watch_later'`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (p *Postgres) DeleteEngagement(ctx context.Context, targetType string, targetID string) error {
	if _, err := p.pool.Exec(ctx, `DELETE FROM likes WHERE target_type = $1 AND target_id = $2`, targetType, targetID); err != nil {
		return err
	}
	if _, err := p.pool.Exec(ctx, `DELETE FROM comments WHERE target_type = $1 AND target_id = $2`, targetType, targetID); err != nil {
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM favorite_items WHERE target_type = $1 AND target_id = $2`, targetType, targetID)
	return err
}
