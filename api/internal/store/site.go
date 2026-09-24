package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (p *Postgres) GetSiteSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := p.pool.QueryRow(ctx, `SELECT value FROM site_settings WHERE key = $1`, key).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func (p *Postgres) SetSiteSetting(ctx context.Context, key, value string) error {
	if key == "" {
		return errors.New("empty setting key")
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO site_settings (key, value, updated_at) VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	return err
}
