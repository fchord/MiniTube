package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type AdminAccount struct {
	PasswordHash string
	TOTPSecret   string
	TOTPPending  string
	TOTPEnrolled bool
}

func (p *Postgres) GetAdminAccount(ctx context.Context) (AdminAccount, error) {
	var a AdminAccount
	err := p.pool.QueryRow(ctx, `
		SELECT password_hash, totp_secret, totp_pending, totp_enrolled
		FROM admin_account WHERE id = 1
	`).Scan(&a.PasswordHash, &a.TOTPSecret, &a.TOTPPending, &a.TOTPEnrolled)
	if errors.Is(err, pgx.ErrNoRows) {
		return AdminAccount{}, ErrNotFound
	}
	return a, err
}

func (p *Postgres) UpsertAdminAccount(ctx context.Context, a AdminAccount) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO admin_account (id, password_hash, totp_secret, totp_pending, totp_enrolled, updated_at)
		VALUES (1, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET
			password_hash = EXCLUDED.password_hash,
			totp_secret = EXCLUDED.totp_secret,
			totp_pending = EXCLUDED.totp_pending,
			totp_enrolled = EXCLUDED.totp_enrolled,
			updated_at = now()
	`, a.PasswordHash, a.TOTPSecret, a.TOTPPending, a.TOTPEnrolled)
	return err
}
