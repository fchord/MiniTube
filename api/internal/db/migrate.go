package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql 002.sql 003.sql 004.sql 005.sql 006.sql 007.sql 008.sql 009.sql 010.sql 011.sql 012.sql 013.sql 014.sql
var schemaFS embed.FS

func Connect(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `SELECT pg_advisory_lock(87451293)`); err != nil {
		return err
	}
	defer pool.Exec(ctx, `SELECT pg_advisory_unlock(87451293)`)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version text PRIMARY KEY,
			applied_at timestamptz NOT NULL DEFAULT now()
		)`); err != nil {
		return err
	}
	if err := apply(ctx, pool, "001", "schema.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "002", "002.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "003", "003.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "004", "004.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "005", "005.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "006", "006.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "007", "007.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "008", "008.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "009", "009.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "010", "010.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "011", "011.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "012", "012.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "013", "013.sql"); err != nil {
		return err
	}
	if err := apply(ctx, pool, "014", "014.sql"); err != nil {
		return err
	}
	return nil
}

func apply(ctx context.Context, pool *pgxpool.Pool, version, file string) error {
	var applied bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
		return err
	}
	if applied {
		return nil
	}
	sql, err := schemaFS.ReadFile(file)
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("apply %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
