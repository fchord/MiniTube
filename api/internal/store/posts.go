package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Post struct {
	ID               uuid.UUID
	AuthorUserID     uuid.UUID
	Author           User
	ChannelID        *uuid.UUID
	Channel          *Channel
	Body             string
	MentionedVideoID *string
	ImageKeys        []string
	LikeCount        int64
	CommentCount     int64
	ViewCount        int64
	CreatedAt        time.Time
}

func (p *Postgres) CreatePost(ctx context.Context, author User, channelID *uuid.UUID, mentioned *string, body string, imageKeys []string) (Post, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Post{}, err
	}
	defer tx.Rollback(ctx)
	var post Post
	err = tx.QueryRow(ctx, `
		INSERT INTO posts (author_user_id, channel_id, body, mentioned_video_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id, author_user_id, channel_id, body, mentioned_video_id, like_count, comment_count, view_count, created_at`,
		author.ID, channelID, body, mentioned,
	).Scan(&post.ID, &post.AuthorUserID, &post.ChannelID, &post.Body, &post.MentionedVideoID, &post.LikeCount, &post.CommentCount, &post.ViewCount, &post.CreatedAt)
	if err != nil {
		return Post{}, err
	}
	for i, key := range imageKeys {
		if _, err := tx.Exec(ctx, `
			INSERT INTO post_images (post_id, object_key, sort_order) VALUES ($1, $2, $3)`, post.ID, key, i); err != nil {
			return Post{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Post{}, err
	}
	post.Author = author
	post.ImageKeys = imageKeys
	if channelID != nil {
		ch, err := p.GetChannelByID(ctx, *channelID)
		if err == nil {
			post.Channel = &ch
		}
	}
	return post, nil
}

func (p *Postgres) GetPost(ctx context.Context, id uuid.UUID) (Post, error) {
	post, err := scanPost(p.pool.QueryRow(ctx, postSelect+" WHERE p.id = $1", id))
	if err != nil {
		return Post{}, err
	}
	return p.hydratePost(ctx, post)
}

func (p *Postgres) DeletePost(ctx context.Context, id, authorID uuid.UUID) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM posts WHERE id = $1 AND author_user_id = $2`, id, authorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ListPostsByAuthor(ctx context.Context, authorID uuid.UUID, limit int) ([]Post, error) {
	return p.listPosts(ctx, postSelect+" WHERE p.author_user_id = $1 ORDER BY p.created_at DESC LIMIT $2", authorID, limit)
}

func (p *Postgres) ListPostsByChannel(ctx context.Context, channelID uuid.UUID, limit int) ([]Post, error) {
	return p.listPosts(ctx, postSelect+" WHERE p.channel_id = $1 ORDER BY p.created_at DESC LIMIT $2", channelID, limit)
}

func (p *Postgres) ListFeedPosts(ctx context.Context, userID uuid.UUID, limit int) ([]Post, error) {
	return p.listPosts(ctx, postSelect+`
		JOIN subscriptions s ON s.channel_id = p.channel_id
		WHERE s.user_id = $1
		ORDER BY p.created_at DESC LIMIT $2`, userID, limit)
}

const postSelect = `
	SELECT p.id, p.author_user_id, p.channel_id, p.body, p.mentioned_video_id, p.like_count, p.comment_count, p.view_count, p.created_at
	FROM posts p`

func scanPost(row interface{ Scan(dest ...any) error }) (Post, error) {
	var post Post
	err := row.Scan(&post.ID, &post.AuthorUserID, &post.ChannelID, &post.Body, &post.MentionedVideoID, &post.LikeCount, &post.CommentCount, &post.ViewCount, &post.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Post{}, ErrNotFound
	}
	return post, err
}

func (p *Postgres) listPosts(ctx context.Context, q string, arg any, limit int) ([]Post, error) {
	rows, err := p.pool.Query(ctx, q, arg, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		post, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, post)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hydrated := make([]Post, 0, len(out))
	for _, post := range out {
		h, err := p.hydratePost(ctx, post)
		if err != nil {
			return nil, err
		}
		hydrated = append(hydrated, h)
	}
	return hydrated, nil
}

func (p *Postgres) hydratePost(ctx context.Context, post Post) (Post, error) {
	u, err := p.GetUserByID(ctx, post.AuthorUserID)
	if err != nil {
		return Post{}, err
	}
	post.Author = u
	if post.ChannelID != nil {
		ch, err := p.GetChannelByID(ctx, *post.ChannelID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Post{}, err
		}
		if err == nil {
			post.Channel = &ch
		}
	}
	rows, err := p.pool.Query(ctx, `SELECT object_key FROM post_images WHERE post_id = $1 ORDER BY sort_order`, post.ID)
	if err != nil {
		return Post{}, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return Post{}, err
		}
		keys = append(keys, k)
	}
	if keys == nil {
		keys = []string{}
	}
	post.ImageKeys = keys
	return post, rows.Err()
}

func (p *Postgres) IncrementPostViews(ctx context.Context, id uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `UPDATE posts SET view_count = view_count + 1 WHERE id = $1`, id)
	return err
}

func (p *Postgres) PostLiked(ctx context.Context, userID, postID uuid.UUID) (bool, error) {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM likes WHERE user_id = $1 AND target_type = 'post' AND target_id = $2)`, userID, postID).Scan(&ok)
	return ok, err
}
