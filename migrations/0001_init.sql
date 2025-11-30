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
