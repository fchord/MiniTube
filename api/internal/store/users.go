package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Postgres struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

func (p *Postgres) Pool() *pgxpool.Pool { return p.pool }

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func uniqueConstraint(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.ConstraintName
	}
	return ""
}

type User struct {
	ID              uuid.UUID
	Username        string
	Email           string
	EmailVerifiedAt *time.Time
	PasswordHash    string
	Nickname        string
	AvatarURL       *string
	Bio             string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Channel struct {
	ID              uuid.UUID
	OwnerUserID     uuid.UUID
	OwnerUsername   string
	Name            string
	Handle          string
	AvatarURL       *string
	BannerURL       *string
	Description     string
	Tags            []string
	Visibility      string
	IsDefault       bool
	SubscriberCount int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	RevokedAt *time.Time
}

type FavoriteList struct {
	ID   uuid.UUID
	Kind string
	Name string
}

type RegisterInput struct {
	Username     string
	Email        string
	PasswordHash string
	Nickname     string
	Handle       string
	ChannelName  string
	RefreshHash  string
	UserAgent    string
	SessionExp   time.Time
	EmailHash    string
	EmailExp     time.Time
}

type RegisterResult struct {
	User    User
	Channel Channel
	Session Session
}

func (p *Postgres) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return RegisterResult{}, err
	}
	defer tx.Rollback(ctx)

	var u User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (username, email, password_hash, nickname)
		VALUES ($1, $2, $3, $4)
		RETURNING id, username, email, email_verified_at, password_hash, nickname, avatar_url, bio, status, created_at, updated_at`,
		in.Username, in.Email, in.PasswordHash, in.Nickname,
	).Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Nickname, &u.AvatarURL, &u.Bio, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if isUnique(err) {
			return RegisterResult{}, fmt.Errorf("%w: %s", ErrConflict, uniqueConstraint(err))
		}
		return RegisterResult{}, err
	}

	ch, err := insertChannel(ctx, tx, u.ID, u.Username, in.ChannelName, in.Handle, true)
	if err != nil {
		if isUnique(err) {
			return RegisterResult{}, fmt.Errorf("%w: %s", ErrConflict, uniqueConstraint(err))
		}
		return RegisterResult{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO favorite_lists (user_id, kind, name) VALUES ($1, 'watch_later', '稍后再看')`, u.ID); err != nil {
		return RegisterResult{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`, u.ID, in.EmailHash, in.EmailExp); err != nil {
		return RegisterResult{}, err
	}

	var s Session
	err = tx.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_hash, user_agent, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, expires_at, revoked_at`,
		u.ID, in.RefreshHash, in.UserAgent, in.SessionExp,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.RevokedAt)
	if err != nil {
		return RegisterResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{User: u, Channel: ch, Session: s}, nil
}

func insertChannel(ctx context.Context, tx pgx.Tx, ownerID uuid.UUID, ownerUsername, name, handle string, isDefault bool) (Channel, error) {
	var ch Channel
	err := tx.QueryRow(ctx, `
		INSERT INTO channels (owner_user_id, name, handle, is_default)
		VALUES ($1, $2, $3, $4)
		RETURNING id, owner_user_id, name, handle, avatar_url, banner_url, description, tags, visibility, is_default, subscriber_count, created_at, updated_at`,
		ownerID, name, handle, isDefault,
	).Scan(&ch.ID, &ch.OwnerUserID, &ch.Name, &ch.Handle, &ch.AvatarURL, &ch.BannerURL, &ch.Description, &ch.Tags, &ch.Visibility, &ch.IsDefault, &ch.SubscriberCount, &ch.CreatedAt, &ch.UpdatedAt)
	ch.OwnerUsername = ownerUsername
	if ch.Tags == nil {
		ch.Tags = []string{}
	}
	return ch, err
}

func (p *Postgres) CreateSession(ctx context.Context, userID uuid.UUID, refreshHash, userAgent string, exp time.Time) (Session, error) {
	var s Session
	err := p.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, refresh_hash, user_agent, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, expires_at, revoked_at`,
		userID, refreshHash, userAgent, exp,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.RevokedAt)
	return s, err
}

func (p *Postgres) GetSession(ctx context.Context, id uuid.UUID) (Session, error) {
	var s Session
	err := p.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at FROM sessions WHERE id = $1`, id,
	).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.RevokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return s, err
}

func (p *Postgres) RevokeSession(ctx context.Context, id uuid.UUID) error {
	_, err := p.pool.Exec(ctx, `UPDATE sessions SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	return err
}

func (p *Postgres) GetUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, userSelect+" WHERE id = $1", id))
}

func (p *Postgres) GetUserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, userSelect+" WHERE username = $1", username))
}

func (p *Postgres) GetUserByIdentifier(ctx context.Context, identifier string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, userSelect+" WHERE username = $1 OR email = $1", identifier))
}

const userSelect = `
	SELECT id, username, email, email_verified_at, password_hash, nickname, avatar_url, bio, status, created_at, updated_at
	FROM users`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.EmailVerifiedAt, &u.PasswordHash, &u.Nickname, &u.AvatarURL, &u.Bio, &u.Status, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (p *Postgres) UpdateMe(ctx context.Context, id uuid.UUID, nickname, bio *string, username *string) (User, error) {
	u, err := scanUser(p.pool.QueryRow(ctx, `
		UPDATE users SET
			nickname = COALESCE($2, nickname),
			bio = COALESCE($3, bio),
			username = COALESCE($4, username),
			updated_at = now()
		WHERE id = $1
		RETURNING id, username, email, email_verified_at, password_hash, nickname, avatar_url, bio, status, created_at, updated_at`,
		id, nickname, bio, username,
	))
	if err != nil && isUnique(err) {
		return User{}, fmt.Errorf("%w: %s", ErrConflict, uniqueConstraint(err))
	}
	return u, err
}

func (p *Postgres) SetAvatarURL(ctx context.Context, id uuid.UUID, url string) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET avatar_url = $2, updated_at = now() WHERE id = $1`, id, url)
	return err
}

func (p *Postgres) SetPasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := p.pool.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, id, hash)
	return err
}

func (p *Postgres) VerifyEmail(ctx context.Context, tokenHash string, now time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var userID uuid.UUID
	var expires time.Time
	var consumed *time.Time
	err = tx.QueryRow(ctx, `
		SELECT user_id, expires_at, consumed_at
		FROM email_verification_tokens
		WHERE token_hash = $1`, tokenHash,
	).Scan(&userID, &expires, &consumed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if consumed != nil {
		return nil
	}
	if !expires.After(now) {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `UPDATE email_verification_tokens SET consumed_at = $2 WHERE token_hash = $1 AND consumed_at IS NULL`, tokenHash, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET email_verified_at = COALESCE(email_verified_at, $2), updated_at = $2 WHERE id = $1`, userID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) ListFavoriteLists(ctx context.Context, userID uuid.UUID) ([]FavoriteList, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, kind, name FROM favorite_lists WHERE user_id = $1 ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FavoriteList
	for rows.Next() {
		var f FavoriteList
		if err := rows.Scan(&f.ID, &f.Kind, &f.Name); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
