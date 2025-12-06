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

-- buckets: 管理每个存储桶的 R2 账号配置和配额
CREATE TABLE IF NOT EXISTS buckets (
    id                     UUID PRIMARY KEY,
    -- 展示名称（面向管理后台）
    name                   TEXT NOT NULL UNIQUE,
    -- R2 中的桶名（不含账号、域名）
    r2_bucket              TEXT NOT NULL,
    -- 每个桶独立的 R2 账号配置
    r2_account_id          TEXT NOT NULL,
    r2_access_key_id       TEXT NOT NULL,
    r2_secret_access_key   TEXT NOT NULL,
    -- 区域，默认 auto，可为 eu / eu-auto 等，具体解析在 storage 层处理
    r2_region              TEXT NOT NULL DEFAULT 'auto',
    -- 可选 endpoint 覆盖（不含桶名），留空时由 account_id + region 推导：
    -- 非欧盟：https://<account_id>.r2.cloudflarestorage.com
    -- 欧盟： https://<account_id>.eu.r2.cloudflarestorage.com
    r2_endpoint            TEXT NOT NULL DEFAULT '',
    -- 最大配额（字节）；0 表示不限
    max_bytes              BIGINT NOT NULL DEFAULT 0,
    -- 当前已用（字节）
    current_bytes          BIGINT NOT NULL DEFAULT 0,
    -- 是否参与自动分配 / 正常使用
    is_active              BOOLEAN NOT NULL DEFAULT TRUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 同一账号内 (account_id, bucket) 组合唯一
    CONSTRAINT uq_buckets_account_bucket UNIQUE (r2_account_id, r2_bucket)
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

-- turnstile_settings: Cloudflare Turnstile 配置，最多一行
CREATE TABLE IF NOT EXISTS turnstile_settings (
    id          SMALLINT PRIMARY KEY DEFAULT 1,
    enabled     BOOLEAN NOT NULL DEFAULT FALSE,
    site_key    TEXT NOT NULL DEFAULT '',
    secret_key  TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
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