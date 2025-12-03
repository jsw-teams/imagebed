package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

// Admin 管理员账号信息
type Admin struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

// ensureAdminTable：正常运行时保证 admins 表存在（不破坏已有数据）
func ensureAdminTable(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("nil db pool")
	}

	_, err := pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS admins (
	id            bigserial    PRIMARY KEY,
	username      text         NOT NULL UNIQUE,
	password_hash text         NOT NULL,
	created_at    timestamptz  NOT NULL DEFAULT NOW()
);
`)
	return err
}

// EnsureInitialAdmin：初始化安装（/setup 第二步）时调用。
// 这里直接 DROP + 重建 admins 表，然后插入管理员账号，保证结构永远正确。
func EnsureInitialAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	if pool == nil {
		return errors.New("nil db pool")
	}
	if username == "" || password == "" {
		return errors.New("username/password cannot be empty")
	}

	// 1) 强制重建 admins 表
	_, err := pool.Exec(ctx, `
DROP TABLE IF EXISTS admins;

CREATE TABLE admins (
	id            bigserial    PRIMARY KEY,
	username      text         NOT NULL UNIQUE,
	password_hash text         NOT NULL,
	created_at    timestamptz  NOT NULL DEFAULT NOW()
);
`)
	if err != nil {
		return err
	}

	// 2) 生成密码哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 3) 插入或更新管理员账号
	_, err = pool.Exec(ctx, `
INSERT INTO admins (username, password_hash)
VALUES ($1, $2)
ON CONFLICT (username)
DO UPDATE SET password_hash = EXCLUDED.password_hash;
`, username, string(hash))

	return err
}

// CheckAdminCredentials：后台登录 & admin_auth 中间件使用
func CheckAdminCredentials(ctx context.Context, pool *pgxpool.Pool, username, password string) (bool, error) {
	if pool == nil {
		return false, errors.New("nil db pool")
	}
	if username == "" || password == "" {
		return false, nil
	}
	if err := ensureAdminTable(ctx, pool); err != nil {
		return false, err
	}

	var hash string
	err := pool.QueryRow(ctx, `
SELECT password_hash
FROM admins
WHERE username = $1;
`, username).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false, nil
	}
	return true, nil
}