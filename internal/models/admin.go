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

// ensureAdminTable：用于运行期（登录校验）保证表存在，**不动已有数据结构**
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

// EnsureInitialAdmin：仅在「初始化安装第二步」调用。
// 这里我们可以比较激进：直接 DROP 掉旧的 admins 表，再用正确结构重建，然后插入管理员。
func EnsureInitialAdmin(ctx context.Context, pool *pgxpool.Pool, username, password string) error {
	if pool == nil {
		return errors.New("nil db pool")
	}
	if username == "" || password == "" {
		return errors.New("username/password cannot be empty")
	}

	// 1) 强制重建 admins 表，保证结构正确
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

	// 3) 插入（或覆盖）管理员账号
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