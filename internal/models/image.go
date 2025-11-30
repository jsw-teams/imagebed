package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Image struct {
	ID          uuid.UUID `json:"id"`
	BucketID    uuid.UUID `json:"bucket_id"`
	ObjectKey   string    `json:"object_key"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	Status      string    `json:"status"` // pending / approved / rejected
	CreatedAt   time.Time `json:"created_at"`
}

func InsertImage(ctx context.Context, tx pgx.Tx, img *Image) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO images (id, bucket_id, object_key, size_bytes, content_type, status)
		VALUES ($1, $2, $3, $4, $5, $6)
	`,
		img.ID,
		img.BucketID,
		img.ObjectKey,
		img.SizeBytes,
		img.ContentType,
		img.Status,
	)
	return err
}

func GetImageByID(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*Image, error) {
	row := db.QueryRow(ctx, `
		SELECT id, bucket_id, object_key, size_bytes, content_type, status, created_at
		FROM images
		WHERE id = $1
	`, id)

	var img Image
	if err := row.Scan(
		&img.ID,
		&img.BucketID,
		&img.ObjectKey,
		&img.SizeBytes,
		&img.ContentType,
		&img.Status,
		&img.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &img, nil
}
