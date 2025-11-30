package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Bucket struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	R2Bucket     string    `json:"r2_bucket"`
	MaxBytes     int64     `json:"max_bytes"`
	CurrentBytes int64     `json:"current_bytes"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func InsertBucket(ctx context.Context, db *pgxpool.Pool, b *Bucket) error {
	_, err := db.Exec(ctx, `
		INSERT INTO buckets (id, name, r2_bucket, max_bytes, current_bytes, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
	`,
		b.ID, b.Name, b.R2Bucket, b.MaxBytes, b.CurrentBytes, b.IsActive,
	)
	return err
}

func ListBuckets(ctx context.Context, db *pgxpool.Pool) ([]Bucket, error) {
	rows, err := db.Query(ctx, `
		SELECT id, name, r2_bucket, max_bytes, current_bytes, is_active, created_at, updated_at
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

func GetBucketByID(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*Bucket, error) {
	row := db.QueryRow(ctx, `
		SELECT id, name, r2_bucket, max_bytes, current_bytes, is_active, created_at, updated_at
		FROM buckets
		WHERE id = $1
	`, id)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
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

func GetBucketForUpdate(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*Bucket, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, name, r2_bucket, max_bytes, current_bytes, is_active, created_at, updated_at
		FROM buckets
		WHERE id = $1
		FOR UPDATE
	`, id)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
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

func UpdateBucketMaxBytes(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, maxBytes int64) (*Bucket, error) {
	row := db.QueryRow(ctx, `
		UPDATE buckets
		SET max_bytes = $2, updated_at = now()
		WHERE id = $1
		RETURNING id, name, r2_bucket, max_bytes, current_bytes, is_active, created_at, updated_at
	`, id, maxBytes)

	var b Bucket
	if err := row.Scan(
		&b.ID,
		&b.Name,
		&b.R2Bucket,
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

func DeleteBucket(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) error {
	_, err := db.Exec(ctx, `DELETE FROM buckets WHERE id = $1`, id)
	return err
}

func IncrementBucketSize(ctx context.Context, tx pgx.Tx, id uuid.UUID, delta int64) error {
	_, err := tx.Exec(ctx, `
		UPDATE buckets
		SET current_bytes = current_bytes + $2,
		    updated_at    = now()
		WHERE id = $1
	`, id, delta)
	return err
}
