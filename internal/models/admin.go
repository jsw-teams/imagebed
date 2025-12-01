// internal/models/admin.go
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

// EnsureInitialAdmin 在初始化安装第二步中调用：
// - 确保 admins 表存在
// - 如果用户名不存在，则插入
// - 如果用户名已存在，则更新密码
func EnsureInitialAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	if pool == nil {
		return errors.New("nil db pool")
	}
	if username == "" || password == "" {
		return errors.New("username/password cannot be empty")
	}
	if err := ensureAdminTable(ctx, pool); err != nil {
		return err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = pool.Exec(ctx, `
INSERT INTO admins (username, password_hash)
VALUES ($1, $2)
ON CONFLICT (username)
DO UPDATE SET password_hash = EXCLUDED.password_hash;
`, username, string(hash))
	return err
}

// CheckAdminCredentials 用于后台登录和 admin_auth 中间件：
// 返回 (true, nil) 表示用户名存在且密码匹配。
// 返回 (false, nil) 表示用户名不存在或密码错误。
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
			// 用户名不存在
			return false, nil
		}
		return false, err
	}

	// 对比密码
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return false, nil
	}
	return true, nil
}