package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Bucket 表示单个存储桶配置及其配额。
// 注意：R2SecretAccessKey 不通过默认 JSON 输出。
type Bucket struct {
	ID                 uuid.UUID `json:"id"`
	Name               string    `json:"name"`
	R2Bucket           string    `json:"r2_bucket"`
	R2AccountID        string    `json:"r2_account_id"`
	R2AccessKeyID      string    `json:"r2_access_key_id"`
	R2SecretAccessKey  string    `json:"-"`              // 不直接暴露给前端，后台接口可用单独结构返回
	R2Region           string    `json:"r2_region"`      // 例如 auto / eu / eu-auto
	R2Endpoint         string    `json:"r2_endpoint"`    // 可选覆盖，不含桶名
	MaxBytes           int64     `json:"max_bytes"`      // 0 表示不限
	CurrentBytes       int64     `json:"current_bytes"`  // 当前已用字节数
	IsActive           bool      `json:"is_active"`      // 是否参与自动分配 / 正常使用
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// InsertBucket 新建一个存储桶记录。
func InsertBucket(ctx context.Context, db *pgxpool.Pool, b *Bucket) error {
	_, err := db.Exec(ctx, `
		INSERT INTO buckets (
			id,
			name,
			r2_bucket,
			r2_account_id,
			r2_access_key_id,
			r2_secret_access_key,
			r2_region,
			r2_endpoint,
			max_bytes,
			current_bytes,
			is_active
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`,
		b.ID,
		b.Name,
		b.R2Bucket,
		b.R2AccountID,
		b.R2AccessKeyID,
		b.R2SecretAccessKey,
		b.R2Region,
		b.R2Endpoint,
		b.MaxBytes,
		b.CurrentBytes,
		b.IsActive,
	)
	return err
}

// ListBuckets 返回按创建时间倒序的全部存储桶。
func ListBuckets(ctx context.Context, db *pgxpool.Pool) ([]Bucket, error) {
	rows, err := db.Query(ctx, `
		SELECT
			id,
			name,
			r2_bucket,
			r2_account_id,
			r2_access_key_id,
			r2_secret_access_key,
			r2_region,
			r2_endpoint,
			max_bytes,
			current_bytes,
			is_active,
			created_at,
			updated_at
		FROM buckets
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []Bucket
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(
			&b.ID,
			&b.Name,
			&b.R2Bucket,
			&b.R2AccountID,
			&b.R2AccessKeyID,
			&b.R2SecretAccessKey,
			&b.R2Region,
			&b.R2Endpoint,
			&b.MaxBytes,
			&b.CurrentBytes,
			&b.IsActive,
			&b.CreatedAt,
			&b.UpdatedAt,
		); err != nil {
			return nil, err
		}
		res = append(res, b)
	}
	return res, rows.Err()
}

// GetBucketByID 根据 ID 查询单个存储桶。
func GetBucketByID(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*Bucket, error) {
	row := db.QueryRow(ctx, `
		SELECT
			id,
			name,
			r2_bucket,
			r2_account_id,
			r2_access_key_id,
			r2_secret_access_key,
			r2_region,
			r2_endpoint,
			max_bytes,
			current_bytes,
			is_active,
			created_at,
			updated_at
		FROM buckets
		WHERE id = $1
	`, id)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
		&b.R2AccountID,
		&b.R2AccessKeyID,
		&b.R2SecretAccessKey,
		&b.R2Region,
		&b.R2Endpoint,
		&b.MaxBytes,
		&b.CurrentBytes,
		&b.IsActive,
		&b.CreatedAt,
		&b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBucketForUpdate 在事务中加锁读取存储桶（FOR UPDATE）。
func GetBucketForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Bucket, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			name,
			r2_bucket,
			r2_account_id,
			r2_access_key_id,
			r2_secret_access_key,
			r2_region,
			r2_endpoint,
			max_bytes,
			current_bytes,
			is_active,
			created_at,
			updated_at
		FROM buckets
		WHERE id = $1
		FOR UPDATE
	`, id)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
		&b.R2AccountID,
		&b.R2AccessKeyID,
		&b.R2SecretAccessKey,
		&b.R2Region,
		&b.R2Endpoint,
		&b.MaxBytes,
		&b.CurrentBytes,
		&b.IsActive,
		&b.CreatedAt,
		&b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &b, nil
}

// UpdateBucketMaxBytes 仅更新桶的最大配额（字节），并返回更新后的记录。
func UpdateBucketMaxBytes(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, maxBytes int64) (*Bucket, error) {
	row := db.QueryRow(ctx, `
		UPDATE buckets
		SET max_bytes = $2,
		    updated_at = now()
		WHERE id = $1
		RETURNING
			id,
			name,
			r2_bucket,
			r2_account_id,
			r2_access_key_id,
			r2_secret_access_key,
			r2_region,
			r2_endpoint,
			max_bytes,
			current_bytes,
			is_active,
			created_at,
			updated_at
	`, id, maxBytes)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
		&b.R2AccountID,
		&b.R2AccessKeyID,
		&b.R2SecretAccessKey,
		&b.R2Region,
		&b.R2Endpoint,
		&b.MaxBytes,
		&b.CurrentBytes,
		&b.IsActive,
		&b.CreatedAt,
		&b.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &b, nil
}

// DeleteBucket 删除指定存储桶记录。
func DeleteBucket(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) error {
	_, err := db.Exec(ctx, `DELETE FROM buckets WHERE id = $1`, id)
	return err
}

// IncrementBucketSize 在事务中增加（或减少）当前已用空间。
func IncrementBucketSize(ctx context.Context, tx pgx.Tx, id uuid.UUID, delta int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE buckets
		SET current_bytes = current_bytes + $2,
		    updated_at    = now()
		WHERE id = $1
	`, id, delta)
	return err
}