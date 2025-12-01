package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TurnstileSettings 用于从数据库读写 Turnstile 配置。
type TurnstileSettings struct {
	Enabled   bool
	SiteKey   string
	SecretKey string
	UpdatedAt time.Time
}

func ensureTurnstileTable(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil db pool")
	}

	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS turnstile_settings (
	id          smallint PRIMARY KEY DEFAULT 1,
	enabled     boolean      NOT NULL DEFAULT false,
	site_key    text         NOT NULL DEFAULT '',
	secret_key  text         NOT NULL DEFAULT '',
	updated_at  timestamptz  NOT NULL DEFAULT NOW()
);
`)
	return err
}

// GetTurnstileSettings 确保表存在并返回当前配置（如果没有记录则创建一条默认记录）。
func GetTurnstileSettings(ctx context.Context, pool *pgxpool.Pool) (*TurnstileSettings, error) {
	if pool == nil {
		return nil, errors.New("nil db pool")
	}
	if err := ensureTurnstileTable(ctx, pool); err != nil {
		return nil, err
	}

	// 确保有一条 id=1 的记录
	_, err := pool.Exec(ctx, `
INSERT INTO turnstile_settings (id, enabled, site_key, secret_key)
VALUES (1, false, '', '')
ON CONFLICT (id) DO NOTHING;
`)
	if err != nil {
		return nil, err
	}

	row := pool.QueryRow(ctx, `
SELECT enabled, site_key, secret_key, updated_at
FROM turnstile_settings
WHERE id = 1;
`)
	var cfg TurnstileSettings
	if err := row.Scan(&cfg.Enabled, &cfg.SiteKey, &cfg.SecretKey, &cfg.UpdatedAt); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// UpsertTurnstileSettings 写入新的配置并返回最新值。
func UpsertTurnstileSettings(ctx context.Context, pool *pgxpool.Pool, enabled bool, siteKey, secretKey string) (*TurnstileSettings, error) {
	if pool == nil {
		return nil, errors.New("nil db pool")
	}
	if err := ensureTurnstileTable(ctx, pool); err != nil {
		return nil, err
	}

	_, err := pool.Exec(ctx, `
INSERT INTO turnstile_settings (id, enabled, site_key, secret_key, updated_at)
VALUES (1, $1, $2, $3, NOW())
ON CONFLICT (id)
DO UPDATE SET enabled   = EXCLUDED.enabled,
              site_key  = EXCLUDED.site_key,
              secret_key= EXCLUDED.secret_key,
              updated_at= NOW();
`, enabled, siteKey, secretKey)
	if err != nil {
		return nil, err
	}

	return GetTurnstileSettings(ctx, pool)
}
