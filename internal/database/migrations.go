package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ddlInit 定义初始数据库结构（相当于原来的 migrations/0001_init.sql）
// 注意：这里都用了 IF NOT EXISTS，重复执行也不会报错。
const ddlInit = `
-- 启用 pgcrypto 扩展，以便使用 gen_random_uuid() 生成 UUID。
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- buckets: 管理 R2 桶配置和配额
CREATE TABLE IF NOT EXISTS buckets (
    id            UUID PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    r2_bucket     TEXT NOT NULL UNIQUE,
    max_bytes     BIGINT NOT NULL,
    current_bytes BIGINT NOT NULL DEFAULT 0,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- images: 存储图片元信息和状态
CREATE TABLE IF NOT EXISTS images (
    id           UUID PRIMARY KEY,
    bucket_id    UUID NOT NULL REFERENCES buckets(id) ON DELETE CASCADE,
    object_key   TEXT NOT NULL,
    size_bytes   BIGINT NOT NULL,
    content_type TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'approved',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_images_bucket_object_key
    ON images(bucket_id, object_key);

-- admins: 管理员账号，用于后台登录 / 管理
CREATE TABLE IF NOT EXISTS admins (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

// RunMigrations 在指定数据库连接上执行内置的初始化 DDL。
// 该函数是幂等的：可安全重复调用。
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("RunMigrations: nil pool")
	}
	if _, err := pool.Exec(ctx, ddlInit); err != nil {
		return fmt.Errorf("RunMigrations: exec init DDL failed: %w", err)
	}
	return nil
}