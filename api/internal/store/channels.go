package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type ChannelUpdate struct {
	Name        *string
	Description *string
	Tags        *[]string
	Visibility  *string
	IsDefault   *bool
}

func (p *Postgres) CreateChannel(ctx context.Context, owner User, name, handle, description string, tags []string) (Channel, error) {
	if tags == nil {
		tags = []string{}
	}
	var ch Channel
	err := p.pool.QueryRow(ctx, `
		INSERT INTO channels (owner_user_id, name, handle, description, tags)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, owner_user_id, name, handle, avatar_url, banner_url, description, tags, visibility, is_default, subscriber_count, created_at, updated_at`,
		owner.ID, name, handle, description, tags,
	).Scan(&ch.ID, &ch.OwnerUserID, &ch.Name, &ch.Handle, &ch.AvatarURL, &ch.BannerURL, &ch.Description, &ch.Tags, &ch.Visibility, &ch.IsDefault, &ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return Channel{}, fmt.Errorf("%w: %s", ErrConflict, uniqueConstraint(err))
		}
		return Channel{}, err
	}
	ch.OwnerUsername = owner.Username
	if ch.Tags == nil {
		ch.Tags = []string{}
	}
	return ch, nil
}

func (p *Postgres) GetChannelByHandle(ctx context.Context, handle string) (Channel, error) {
	ch, err := scanChannel(p.pool.QueryRow(ctx, channelSelect+" WHERE c.handle = $1", handle))
	if err != nil {
		return Channel{}, err
	}
	return ch, nil
}

func (p *Postgres) GetChannelByID(ctx context.Context, id uuid.UUID) (Channel, error) {
	return scanChannel(p.pool.QueryRow(ctx, channelSelect+" WHERE c.id = $1", id))
}

func (p *Postgres) DefaultChannel(ctx context.Context, userID uuid.UUID) (Channel, error) {
	return scanChannel(p.pool.QueryRow(ctx, channelSelect+" WHERE c.owner_user_id = $1 AND c.is_default", userID))
}

func (p *Postgres) ListChannelsByOwner(ctx context.Context, userID uuid.UUID) ([]Channel, error) {
	rows, err := p.pool.Query(ctx, channelSelect+" WHERE c.owner_user_id = $1 ORDER BY c.is_default DESC, c.created_at", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		ch, err := scanChannelRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	if out == nil {
		out = []Channel{}
	}
	return out, rows.Err()
}

const channelSelect = `
	SELECT c.id, c.owner_user_id, u.username, c.name, c.handle, c.avatar_url, c.banner_url,
	       c.description, c.tags, c.visibility, c.is_default, c.subscriber_count, c.created_at, c.updated_at
	FROM channels c
	JOIN users u ON u.id = c.owner_user_id`

func scanChannel(row pgx.Row) (Channel, error) {
	var ch Channel
	err := row.Scan(&ch.ID, &ch.OwnerUserID, &ch.OwnerUsername, &ch.Name, &ch.Handle, &ch.AvatarURL, &ch.BannerURL, &ch.Description, &ch.Tags, &ch.Visibility, &ch.IsDefault, &ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if ch.Tags == nil {
		ch.Tags = []string{}
	}
	return ch, err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanChannelInto(row rowScanner, extra ...any) (Channel, error) {
	var ch Channel
	dest := []any{&ch.ID, &ch.OwnerUserID, &ch.OwnerUsername, &ch.Name, &ch.Handle, &ch.AvatarURL, &ch.BannerURL, &ch.Description, &ch.Tags, &ch.Visibility, &ch.IsDefault, &ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt}
	dest = append(dest, extra...)
	err := row.Scan(dest...)
	if ch.Tags == nil {
		ch.Tags = []string{}
	}
	return ch, err
}

func scanChannelRow(row rowScanner) (Channel, error) {
	return scanChannelInto(row)
}

func (p *Postgres) UpdateChannel(ctx context.Context, handle string, ownerID uuid.UUID, upd ChannelUpdate) (Channel, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	var currentOwner uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id, owner_user_id FROM channels WHERE handle = $1`, handle).Scan(&id, &currentOwner)
	if errors.Is(err, pgx.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	if err != nil {
		return Channel{}, err
	}
	if currentOwner != ownerID {
		return Channel{}, ErrForbidden
	}

	if upd.IsDefault != nil && *upd.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE channels SET is_default = (id = $2), updated_at = now() WHERE owner_user_id = $1`, ownerID, id); err != nil {
			return Channel{}, err
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE channels SET
			name = COALESCE($2, name),
			description = COALESCE($3, description),
			tags = COALESCE($4, tags),
			visibility = COALESCE($5, visibility),
			updated_at = now()
		WHERE id = $1`,
		id, upd.Name, upd.Description, upd.Tags, upd.Visibility,
	)
	if err != nil {
		return Channel{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Channel{}, err
	}
	return p.GetChannelByHandle(ctx, handle)
}

var ErrForbidden = errors.New("forbidden")

func (p *Postgres) HandleTaken(ctx context.Context, handle string) (bool, error) {
	var exists bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM channels WHERE handle = $1)`, handle).Scan(&exists)
	return exists, err
}

func (p *Postgres) Subscribe(ctx context.Context, userID uuid.UUID, channelID uuid.UUID) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		INSERT INTO subscriptions (user_id, channel_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, userID, channelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `UPDATE channels SET subscriber_count = subscriber_count + 1 WHERE id = $1`, channelID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) Unsubscribe(ctx context.Context, userID uuid.UUID, channelID uuid.UUID) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM subscriptions WHERE user_id = $1 AND channel_id = $2`, userID, channelID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		if _, err := tx.Exec(ctx, `UPDATE channels SET subscriber_count = GREATEST(subscriber_count - 1, 0) WHERE id = $1`, channelID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) IsSubscribed(ctx context.Context, userID, channelID uuid.UUID) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE user_id = $1 AND channel_id = $2)`, userID, channelID).Scan(&ok)
	return ok, err
}

type ChannelContentCounts struct {
	Videos int64
	Shorts int64
	Lives  int64
	Posts  int64
}

func (p *Postgres) CountChannelContent(ctx context.Context, channelID uuid.UUID, ownerView bool) (ChannelContentCounts, error) {
	q := `
		SELECT
			(SELECT count(*) FROM videos v WHERE v.channel_id = $1 AND v.kind = 'long'` + visibleVideoSQL(ownerView) + `),
			(SELECT count(*) FROM videos v WHERE v.channel_id = $1 AND v.kind = 'short'` + visibleVideoSQL(ownerView) + `),
			(SELECT count(*) FROM live_streams l WHERE l.channel_id = $1` + visibleLiveSQL(ownerView) + `),
			(SELECT count(*) FROM posts WHERE channel_id = $1)`
	var out ChannelContentCounts
	err := p.pool.QueryRow(ctx, q, channelID).Scan(&out.Videos, &out.Shorts, &out.Lives, &out.Posts)
	return out, err
}

func visibleVideoSQL(ownerView bool) string {
	if ownerView {
		return ` AND v.status = 'ready' AND v.visibility IN ('public', 'private')`
	}
	return ` AND v.status = 'ready' AND v.visibility = 'public'`
}

type SubscribedChannel struct {
	Channel
	SubscribedAt time.Time
}

func (p *Postgres) ListSubscriptions(ctx context.Context, userID uuid.UUID, cursorTime *time.Time, cursorID *uuid.UUID, limit int) ([]SubscribedChannel, error) {
	q := `
		SELECT c.id, c.owner_user_id, u.username, c.name, c.handle, c.avatar_url, c.banner_url,
		       c.description, c.tags, c.visibility, c.is_default, c.subscriber_count, c.created_at, c.updated_at,
		       s.created_at
		FROM channels c
		JOIN users u ON u.id = c.owner_user_id
		JOIN subscriptions s ON s.channel_id = c.id
		WHERE s.user_id = $1`
	args := []any{userID}
	if cursorTime != nil && cursorID != nil {
		q += ` AND (s.created_at, c.id) < ($2, $3)`
		args = append(args, *cursorTime, *cursorID)
	}
	q += ` ORDER BY s.created_at DESC, c.id DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SubscribedChannel
	for rows.Next() {
		var at time.Time
		ch, err := scanChannelInto(rows, &at)
		if err != nil {
			return nil, err
		}
		out = append(out, SubscribedChannel{Channel: ch, SubscribedAt: at})
	}
	if out == nil {
		out = []SubscribedChannel{}
	}
	return out, rows.Err()
}

func itoa(n int) string {
	if n < 0 {
		return "0"
	}
	return fmt.Sprintf("%d", n)
}
